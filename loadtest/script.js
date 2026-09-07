/**
 * k6 load test for the jigsaw backend.
 *
 *   k6 run loadtest/k6-api-test.js                      # smoke, 1 VU
 *   k6 run -e PROFILE=load  loadtest/k6-api-test.js     # ramp to 20 VUs
 *   k6 run -e PROFILE=stress loadtest/k6-api-test.js    # ramp to 100 VUs
 *   k6 run -e PROFILE=spike  loadtest/k6-api-test.js    # sudden 100 VUs
 *
 * Environment:
 *   BASE_URL   default http://localhost:8080
 *   USERS      size of the test-account pool (default 5)
 *   PASSWORD   password for those accounts (default "loadtest-pw-123")
 *   EMAIL_FMT  account email pattern, %d is the index (default k6-load-%d@loadtest.local)
 *   WRITE      "1" to include the mutating expedition flow (writes to the DB!)
 *
 * IMPORTANT — the server rate-limits per IP, and k6 is one IP:
 *
 *   RATE_LIMIT_PER_SEC=20    RATE_LIMIT_BURST=40    (all routes)
 *   AUTH_RATE_PER_SEC=0.25   AUTH_RATE_BURST=8      (login/register only)
 *
 * At defaults anything past ~20 req/s is answered 429 and you will be measuring
 * the limiter, not the backend. That is a valid thing to test (see the
 * `rate_limited` metric), but for a throughput run raise the limits on the
 * target first:
 *
 *   RATE_LIMIT_PER_SEC=100000 RATE_LIMIT_BURST=100000 \
 *   AUTH_RATE_PER_SEC=1000    AUTH_RATE_BURST=1000    ./server
 *
 * Never point this at production.
 */
import http from 'k6/http';
import { check, group, sleep } from 'k6';
import { Counter, Rate, Trend } from 'k6/metrics';

const BASE_URL = (__ENV.BASE_URL || 'http://gameghepanhbackend-production.up.railway.app').replace(/\/+$/, '');
const USER_COUNT = parseInt(__ENV.USERS || '5', 10);
const PASSWORD = __ENV.PASSWORD || 'iop890IOP*()';
const EMAIL_FMT = __ENV.EMAIL_FMT || 'test3@gmail.com';
const WRITE_ENABLED = __ENV.WRITE === '1';
const PROFILE = __ENV.PROFILE || 'smoke';

// --- Metrics -----------------------------------------------------------------

// errors excludes 429 on purpose: a rate-limited response means the server is
// healthy and enforcing policy, which is not the same failure as a 5xx.
const errors = new Rate('errors');
const rateLimited = new Counter('rate_limited');
const endpointDuration = new Trend('endpoint_duration', true);

// --- Load profiles -----------------------------------------------------------

const profiles = {
  smoke: {
    executor: 'constant-vus',
    vus: 1,
    duration: '30s',
  },
  load: {
    executor: 'ramping-vus',
    startVUs: 1,
    stages: [
      { duration: '30s', target: 20 },
      { duration: '2m', target: 20 },
      { duration: '30s', target: 0 },
    ],
    gracefulRampDown: '20s',
  },
  stress: {
    executor: 'ramping-vus',
    startVUs: 1,
    stages: [
      { duration: '1m', target: 50 },
      { duration: '2m', target: 100 },
      { duration: '1m', target: 0 },
    ],
    gracefulRampDown: '30s',
  },
  spike: {
    executor: 'ramping-vus',
    startVUs: 1,
    stages: [
      { duration: '10s', target: 100 },
      { duration: '1m', target: 100 },
      { duration: '10s', target: 1 },
    ],
    gracefulRampDown: '20s',
  },
};

if (!profiles[PROFILE]) {
  throw new Error(`unknown PROFILE "${PROFILE}" (smoke|load|stress|spike)`);
}

export const options = {
  scenarios: { [PROFILE]: profiles[PROFILE] },
  thresholds: {
    // Real failures only — 429s are counted separately in `rate_limited`.
    errors: ['rate<0.01'],
    http_req_duration: ['p(95)<500', 'p(99)<1500'],
    // Per-endpoint budgets. The expedition campaign builds the whole level
    // list per request, so it gets more headroom than the trivial reads.
    'endpoint_duration{endpoint:GET /me}': ['p(95)<300'],
    'endpoint_duration{endpoint:GET /expedition}': ['p(95)<800'],
    'endpoint_duration{endpoint:GET /stories}': ['p(95)<400'],
    'endpoint_duration{endpoint:GET /leaderboard}': ['p(95)<500'],
  },
};

// --- Helpers -----------------------------------------------------------------

function emailFor(i) {
  return EMAIL_FMT.replace('%d', String(i));
}

function randomInt(min, max) {
  return Math.floor(Math.random() * (max - min + 1)) + min;
}

/**
 * One measured request. `expected` lists the statuses that mean "worked" —
 * e.g. /challenges/today legitimately 404s on a day with no challenge, so a
 * 404 there is not an error.
 *
 * 429 is never an error: it is recorded in `rate_limited` and skipped, so a
 * throttled run reports "the limiter kicked in" instead of "the API is broken".
 */
function call(name, method, path, { token, body, expected = [200] } = {}) {
  const params = {
    headers: {
      'Content-Type': 'application/json',
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
    },
    tags: { endpoint: name },
  };

  const res = http.request(method, `${BASE_URL}${path}`, body ? JSON.stringify(body) : null, params);

  if (res.status === 429) {
    rateLimited.add(1, { endpoint: name });
    return res;
  }

  endpointDuration.add(res.timings.duration, { endpoint: name });
  const ok = check(res, {
    [`${name} -> ${expected.join('/')}`]: (r) => expected.includes(r.status),
  });
  errors.add(!ok, { endpoint: name });

  if (!ok) {
    console.error(`${name} unexpected ${res.status}: ${String(res.body).slice(0, 200)}`);
  }
  return res;
}

function parse(res) {
  try {
    return res.json();
  } catch (_) {
    return null;
  }
}

// --- Setup: build the account pool ------------------------------------------

/**
 * Logs in USER_COUNT accounts, registering any that do not exist yet, and
 * returns their tokens. Runs once before the test.
 *
 * This is paced and retried because the auth limiter is far stricter than the
 * global one (0.25/s, burst 8 by default): a pool larger than the burst would
 * otherwise fail outright on a target with stock settings.
 */
export function setup() {
  const health = http.get(`${BASE_URL}/healthz`);
  if (health.status !== 200) {
    throw new Error(`backend not reachable at ${BASE_URL}/healthz (status ${health.status})`);
  }

  const tokens = [];
  for (let i = 0; i < USER_COUNT; i++) {
    const email = emailFor(i);
    const token = authenticate(email);
    if (token) {
      tokens.push(token);
    } else {
      console.warn(`could not authenticate ${email}, skipping`);
    }
  }

  if (tokens.length === 0) {
    throw new Error(
      'no test accounts available — check credentials, or raise AUTH_RATE_PER_SEC/AUTH_RATE_BURST on the target',
    );
  }
  console.log(`setup: ${tokens.length}/${USER_COUNT} accounts ready at ${BASE_URL}`);
  return { tokens, writeEnabled: WRITE_ENABLED };
}

/** Login, falling back to register, with backoff on 429 from the auth limiter. */
function authenticate(email) {
  const headers = { 'Content-Type': 'application/json' };

  for (let attempt = 0; attempt < 6; attempt++) {
    const login = http.post(
      `${BASE_URL}/api/v1/auth/login`,
      JSON.stringify({ email, password: PASSWORD }),
      { headers },
    );
    if (login.status === 200) {
      return parse(login).token;
    }
    if (login.status === 429) {
      sleep(4 * (attempt + 1)); // auth limiter refills at ~0.25/s
      continue;
    }

    // 401 -> the account does not exist yet (or the password changed).
    const register = http.post(
      `${BASE_URL}/api/v1/auth/register`,
      JSON.stringify({ email, password: PASSWORD, confirmPassword: PASSWORD }),
      { headers },
    );
    if (register.status === 201) {
      return parse(register).token;
    }
    if (register.status === 429) {
      sleep(4 * (attempt + 1));
      continue;
    }
    if (register.status === 409) {
      // Exists with a different password — nothing this script can do.
      console.error(`${email} exists but the password does not match; set PASSWORD or EMAIL_FMT`);
      return null;
    }
    console.error(`register ${email} failed: ${register.status} ${String(register.body).slice(0, 200)}`);
    return null;
  }
  return null;
}

// --- The VU journey ----------------------------------------------------------

export default function (data) {
  // Spread VUs across the pool so they are not all the same user (which would
  // make the DB row and its progress a hot spot that no real traffic has).
  const token = data.tokens[(__VU - 1) % data.tokens.length];

  group('session bootstrap', () => {
    call('GET /me', 'GET', '/api/v1/me', { token });
    call('GET /me/points', 'GET', '/api/v1/me/points', { token });
  });

  group('home reads', () => {
    // 404 is normal when no challenge is scheduled for today.
    call('GET /challenges/today', 'GET', '/api/v1/challenges/today', {
      token,
      expected: [200, 404],
    });
    call('GET /leaderboard', 'GET', '/api/v1/leaderboard', { token });
    call('GET /checkin', 'GET', '/api/v1/checkin', { token });
  });

  sleep(randomInt(1, 3));

  group('expedition', () => {
    const res = call('GET /expedition', 'GET', '/api/v1/expedition', { token });
    if (data.writeEnabled && res.status === 200) {
      exerciseExpeditionWrite(token, parse(res));
    }
  });

  group('stories', () => {
    const res = call('GET /stories', 'GET', '/api/v1/stories', { token });
    const stories = (parse(res) || {}).stories || [];
    if (stories.length > 0) {
      // Detail of a random story — locked ones return metadata without pages,
      // which is a 200 either way.
      const story = stories[randomInt(0, stories.length - 1)];
      call('GET /stories/{id}', 'GET', `/api/v1/stories/${story.id}`, { token });
    }
  });

  group('dungeons', () => {
    call('GET /dungeons', 'GET', '/api/v1/dungeons', { token });
    call('GET /dungeon-treasures', 'GET', '/api/v1/dungeon-treasures', { token });
  });

  group('profile', () => {
    call('GET /me/stats', 'GET', '/api/v1/me/stats', { token });
    call('GET /me/history', 'GET', '/api/v1/me/history', { token });
    call('GET /collection', 'GET', '/api/v1/collection', { token });
  });

  sleep(randomInt(2, 5));
}

/**
 * Opt-in write path (WRITE=1). Starts an unlocked level that has an image and
 * then fails it.
 *
 * `fail` is chosen over `complete` deliberately: failing is retryable and
 * awards nothing, so repeated runs do not inflate anyone's XP, stars or
 * leaderboard position. `complete` would permanently alter progression.
 */
function exerciseExpeditionWrite(token, state) {
  const levels = (state || {}).levels || [];
  const playable = levels.filter((l) => l.unlocked && l.imageUrl && l.status !== 'completed');
  if (playable.length === 0) {
    return;
  }
  const level = playable[randomInt(0, playable.length - 1)];

  // 403 is expected and fine: another VU may have taken the level's slot, or
  // the image was removed between the read and this call.
  const started = call('POST /expedition/{n}/start', 'POST', `/api/v1/expedition/${level.index}/start`, {
    token,
    expected: [200, 403],
  });
  if (started.status !== 200) {
    return;
  }

  sleep(1);
  call('POST /expedition/{n}/fail', 'POST', `/api/v1/expedition/${level.index}/fail`, {
    token,
    body: { correctPieces: randomInt(0, 4) },
  });
}

// --- Summary -----------------------------------------------------------------

export function teardown(data) {
  console.log(
    `teardown: ${data.tokens.length} accounts used. ` +
      'Test accounts are left in place so the next run can reuse them ' +
      '(the auth limiter makes re-registering slow).',
  );
}
