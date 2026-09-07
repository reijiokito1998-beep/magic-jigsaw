package config

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

// minJWTSecretLen is the minimum acceptable length for the signing secret. A
// short secret is brute-forceable and would let anyone forge session tokens.
const minJWTSecretLen = 32

// Config holds all runtime configuration, loaded from environment variables.
type Config struct {
	AppEnv      string
	Port        string
	CORSOrigins []string
	DatabaseURL string
	JWTSecret   string
	JWTTTL      time.Duration
	AdminEmails []string

	// Operational hardening / tuning.
	DBMaxConns      int32
	EnableSwagger   bool
	TrustProxy      bool
	RateLimitPerSec float64 // global per-IP sustained rate
	RateLimitBurst  float64 // global per-IP burst
	// The auth endpoints are capped per IP on a rolling hour rather than per
	// second: both are cheap to script and expensive to abuse (spam accounts,
	// credential stuffing), and legitimate users hit them a handful of times a
	// day. The *Burst values are how many of the hourly budget a client may
	// spend back-to-back.
	RegisterRatePerHour float64
	RegisterRateBurst   float64
	LoginRatePerHour    float64
	LoginRateBurst      float64

	// Observability.
	LogLevel  string // debug|info|warn|error
	LogFormat string // json (machine-parsable) | text (human-readable)
	// MetricsEnabled exposes the Prometheus scrape endpoint.
	MetricsEnabled bool
	MetricsPath    string
	// MetricsToken, when set, is the bearer token a scrape must present.
	// /metrics reveals route names and error rates, so leave it unset only when
	// the endpoint is unreachable from the internet.
	MetricsToken string

	CloudinaryURL       string
	CloudinaryCloudName string
	CloudinaryAPIKey    string
	CloudinaryAPISecret string
	CloudinaryFolder    string

	// In-app purchase verification. All optional: a platform whose credentials
	// are missing simply has no verifier wired up, so its purchases always fail
	// verification instead of crashing the server on a dev machine.
	AppleSharedSecret string // App Store Connect app-specific shared secret (verifyReceipt "password")
	AppleBundleID     string // expected receipt bundle_id; empty disables the check
	GooglePackageName string // Android applicationId, e.g. com.example.jigsaw
	// GoogleServiceAccountJSON is either the raw service-account key JSON
	// (detected by a leading "{") or a path to a file containing it.
	GoogleServiceAccountJSON string
}

// IsProduction reports whether the app is running in a production environment.
func (c *Config) IsProduction() bool {
	return strings.EqualFold(c.AppEnv, "production") || strings.EqualFold(c.AppEnv, "prod")
}

// LoadDotEnv loads a .env file into the process environment without overriding
// variables already set. It is exported so main can initialise logging from
// these values *before* Load runs, ensuring even config-loading lines are
// structured. Calling it twice is harmless.
func LoadDotEnv(path string) { loadDotEnv(path) }

// LogSettings returns the logging level and format from the environment, with
// the same defaults Load applies (JSON in production, text elsewhere). Used by
// main to configure logging before the full config is parsed.
func LogSettings() (level, format string) {
	level = getEnv("LOG_LEVEL", "info")
	prod := strings.EqualFold(os.Getenv("APP_ENV"), "production") || strings.EqualFold(os.Getenv("APP_ENV"), "prod")
	format = "text"
	if prod {
		format = "json"
	}
	return level, getEnv("LOG_FORMAT", format)
}

// Load reads configuration from the process environment. If a .env file is
// present in the working directory it is loaded first (without overriding
// variables already set in the environment).
func Load() (*Config, error) {
	loadDotEnv(".env")

	cfg := &Config{
		AppEnv:              getEnv("APP_ENV", "development"),
		Port:                getEnv("PORT", "8080"),
		CORSOrigins:         splitCSV(getEnv("CORS_ORIGINS", "*")),
		DatabaseURL:         os.Getenv("DATABASE_URL"),
		JWTSecret:           os.Getenv("JWT_SECRET"),
		AdminEmails:         splitCSV(strings.ToLower(os.Getenv("ADMIN_EMAILS"))),
		DBMaxConns:          int32(getEnvInt("DB_MAX_CONNS", 20)),
		TrustProxy:          getEnvBool("TRUST_PROXY", false),
		RateLimitPerSec:     getEnvFloat("RATE_LIMIT_PER_SEC", 20),
		RateLimitBurst:      getEnvFloat("RATE_LIMIT_BURST", 40),
		RegisterRatePerHour: getEnvFloat("REGISTER_RATE_PER_HOUR", 5),
		RegisterRateBurst:   getEnvFloat("REGISTER_RATE_BURST", 5),
		LoginRatePerHour:    getEnvFloat("LOGIN_RATE_PER_HOUR", 20),
		LoginRateBurst:      getEnvFloat("LOGIN_RATE_BURST", 20),
		LogLevel:            getEnv("LOG_LEVEL", "info"),
		MetricsEnabled:      getEnvBool("METRICS_ENABLED", true),
		MetricsPath:         getEnv("METRICS_PATH", "/metrics"),
		MetricsToken:        os.Getenv("METRICS_TOKEN"),
		CloudinaryURL:       os.Getenv("CLOUDINARY_URL"),
		CloudinaryCloudName: os.Getenv("CLOUDINARY_CLOUD_NAME"),
		CloudinaryAPIKey:    os.Getenv("CLOUDINARY_API_KEY"),
		CloudinaryAPISecret: os.Getenv("CLOUDINARY_API_SECRET"),
		CloudinaryFolder:    getEnv("CLOUDINARY_FOLDER", "jigsaw"),

		AppleSharedSecret:        os.Getenv("APPLE_SHARED_SECRET"),
		AppleBundleID:            os.Getenv("APPLE_BUNDLE_ID"),
		GooglePackageName:        os.Getenv("GOOGLE_PACKAGE_NAME"),
		GoogleServiceAccountJSON: os.Getenv("GOOGLE_SERVICE_ACCOUNT_JSON"),
	}

	// Swagger is on by default outside production; ENABLE_SWAGGER overrides.
	cfg.EnableSwagger = getEnvBool("ENABLE_SWAGGER", !cfg.IsProduction())

	// JSON logs in production (log aggregators parse them); readable text
	// locally. LOG_FORMAT overrides either way.
	defaultFormat := "text"
	if cfg.IsProduction() {
		defaultFormat = "json"
	}
	cfg.LogFormat = getEnv("LOG_FORMAT", defaultFormat)

	if !strings.HasPrefix(cfg.MetricsPath, "/") {
		cfg.MetricsPath = "/" + cfg.MetricsPath
	}

	ttl, err := time.ParseDuration(getEnv("JWT_TTL", "24h"))
	if err != nil {
		return nil, fmt.Errorf("invalid JWT_TTL: %w", err)
	}
	cfg.JWTTTL = ttl

	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	if cfg.JWTSecret == "" {
		return nil, fmt.Errorf("JWT_SECRET is required")
	}
	if len(cfg.JWTSecret) < minJWTSecretLen {
		return nil, fmt.Errorf("JWT_SECRET must be at least %d characters (use a long random value)", minJWTSecretLen)
	}
	if cfg.CloudinaryURL == "" && cfg.CloudinaryCloudName == "" {
		return nil, fmt.Errorf("Cloudinary credentials are required (set CLOUDINARY_URL or CLOUDINARY_CLOUD_NAME/API_KEY/API_SECRET)")
	}

	log.Printf("config: loaded app_env=%s port=%s cors_origins=%v enable_swagger=%t trust_proxy=%t db_max_conns=%d jwt_ttl=%s",
		cfg.AppEnv, cfg.Port, cfg.CORSOrigins, cfg.EnableSwagger, cfg.TrustProxy, cfg.DBMaxConns, cfg.JWTTTL)
	log.Printf("config: rate limits per_sec=%v burst=%v register_per_hour=%v register_burst=%v login_per_hour=%v login_burst=%v",
		cfg.RateLimitPerSec, cfg.RateLimitBurst,
		cfg.RegisterRatePerHour, cfg.RegisterRateBurst,
		cfg.LoginRatePerHour, cfg.LoginRateBurst)
	log.Printf("config: observability log_level=%s log_format=%s metrics_enabled=%t metrics_path=%s metrics_token=%s",
		cfg.LogLevel, cfg.LogFormat, cfg.MetricsEnabled, cfg.MetricsPath, setOrMissing(cfg.MetricsToken))
	log.Printf("config: database_url=%s jwt_secret=%s cloudinary_url=%s cloudinary_api_key=%s cloudinary_api_secret=%s",
		setOrMissing(cfg.DatabaseURL), setOrMissing(cfg.JWTSecret), setOrMissing(cfg.CloudinaryURL),
		setOrMissing(cfg.CloudinaryAPIKey), setOrMissing(cfg.CloudinaryAPISecret))
	log.Printf("config: cloudinary_cloud_name=%s cloudinary_folder=%s admin_emails_count=%d",
		setOrMissing(cfg.CloudinaryCloudName), cfg.CloudinaryFolder, len(cfg.AdminEmails))
	log.Printf("config: apple_shared_secret=%s apple_bundle_id=%s google_package_name=%s google_service_account=%s",
		setOrMissing(cfg.AppleSharedSecret), setOrMissing(cfg.AppleBundleID),
		setOrMissing(cfg.GooglePackageName), setOrMissing(cfg.GoogleServiceAccountJSON))

	// Loud warning for the most common production foot-gun: wildcard CORS.
	if cfg.IsProduction() {
		for _, o := range cfg.CORSOrigins {
			if o == "*" {
				log.Println("WARNING: CORS_ORIGINS is '*' in production; set an explicit allow-list of origins")
			}
		}
		if len(cfg.CORSOrigins) == 0 {
			log.Println("WARNING: CORS_ORIGINS is empty in production; all origins will be allowed")
		}
		if cfg.AppleSharedSecret == "" {
			log.Println("WARNING: APPLE_SHARED_SECRET is not set in production; App Store purchases cannot be credited")
		}
		if cfg.GooglePackageName == "" || cfg.GoogleServiceAccountJSON == "" {
			log.Println("WARNING: GOOGLE_PACKAGE_NAME/GOOGLE_SERVICE_ACCOUNT_JSON are not set in production; Google Play purchases cannot be credited")
		}
	}

	return cfg, nil
}

// IsAdminEmail reports whether the given email is configured as an admin.
func (c *Config) IsAdminEmail(email string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	for _, a := range c.AdminEmails {
		if a == email {
			return true
		}
	}
	return false
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
		log.Printf("WARNING: invalid %s=%q, using default %d", key, v, fallback)
	}
	return fallback
}

func getEnvFloat(key string, fallback float64) float64 {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
		log.Printf("WARNING: invalid %s=%q, using default %v", key, v, fallback)
	}
	return fallback
}

func getEnvBool(key string, fallback bool) bool {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
		log.Printf("WARNING: invalid %s=%q, using default %v", key, v, fallback)
	}
	return fallback
}

// setOrMissing reports whether a secret-like value was provided, without ever
// exposing its actual value in logs.
func setOrMissing(v string) string {
	if v == "" {
		return "missing"
	}
	return "set"
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// loadDotEnv is a tiny .env parser: KEY=VALUE lines, ignoring comments and
// blanks. Existing environment variables take precedence.
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.Trim(strings.TrimSpace(val), `"'`)
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, val)
		}
	}
}
