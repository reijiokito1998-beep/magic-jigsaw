-- In-app purchases (IAP): the audit log of every store purchase we have
-- credited unlock points for. Points are NEVER granted from the client's
-- claimed productId alone -- the server first verifies the receipt/token with
-- Apple (verifyReceipt) or Google (Play Developer API), then records the
-- verified transaction here and bumps users.unlock_points in one transaction.
--
-- UNIQUE (platform, transaction_id) is what makes crediting idempotent: a
-- client that retries confirmPurchase (app restart before completePurchase was
-- acknowledged, or restorePurchases) replays the same store transaction id, the
-- INSERT ... ON CONFLICT DO NOTHING affects 0 rows, and no points are granted
-- twice. transaction_id is the Google Play purchase token or the Apple
-- transaction_id -- whatever uniquely identifies this one purchase event.
--
-- points_credited is stored (rather than derived from product_id) so the ledger
-- stays truthful even if the server-side product catalogue changes its payout
-- later.

CREATE TABLE IF NOT EXISTS purchases (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    product_id      TEXT NOT NULL,
    platform        TEXT NOT NULL, -- 'app_store' | 'google_play'
    transaction_id  TEXT NOT NULL,
    points_credited INT  NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (platform, transaction_id)
);

-- Supports a future "my purchase history" query.
CREATE INDEX IF NOT EXISTS idx_purchases_user ON purchases(user_id, created_at DESC);
