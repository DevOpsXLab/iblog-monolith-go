// Package config loads settings from .env and the environment (oneenv).
package config

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/bakhod1r/oneenv"
)

// Development-only values. Validate refuses them outside APP_ENV=dev.
const (
	DevMFAKey         = "dev-mfa-key-change-me-0123456789abcdef"
	DevUnsubscribeKey = "dev-unsubscribe-key-change-me-0123456789"
)

// weakSecrets are well-known defaults that must never reach production.
var weakSecrets = map[string]bool{
	"": true, "dev-mfa-key-change-me": true, DevMFAKey: true, DevUnsubscribeKey: true,
	"admin12345": true, "minio12345": true, "blog": true, "admin": true, "password": true,
}

type Config struct {
	Env         string `env:"APP_ENV" default:"production" desc:"dev or production; production refuses default secrets"`
	Port        string `env:"PORT" default:"8080" desc:"listen port"`
	DatabaseURL string `env:"DATABASE_URL,secret" default:"postgres://blog:blog@localhost:5433/blog?sslmode=disable" desc:"Postgres DSN"`
	RedisURL    string `env:"REDIS_URL,secret" default:"redis://localhost:6380/0" desc:"Redis URL (cache, sessions, queue, pub/sub)"`

	AdminUsername string `env:"ADMIN_USERNAME" desc:"super admin seeded on start (with ADMIN_EMAIL, ADMIN_PASSWORD)"`
	AdminEmail    string `env:"ADMIN_EMAIL" desc:"super admin email"`
	AdminPassword string `env:"ADMIN_PASSWORD,secret" desc:"super admin password"`

	S3Endpoint  string `env:"S3_ENDPOINT" default:"localhost:9000" desc:"MinIO endpoint host:port"`
	S3AccessKey string `env:"S3_ACCESS_KEY,secret" default:"minio" desc:"MinIO access key"`
	S3SecretKey string `env:"S3_SECRET_KEY,secret" default:"minio12345" desc:"MinIO secret key"`
	S3Bucket    string `env:"S3_BUCKET" default:"uploads" desc:"bucket for uploads"`
	S3UseSSL    bool   `env:"S3_USE_SSL" default:"false" desc:"use HTTPS to reach MinIO"`

	SMTPAddr string `env:"SMTP_ADDR" desc:"SMTP host:port (Mailpit in dev); empty keeps mail in memory"`
	MailFrom string `env:"MAIL_FROM" default:"no-reply@iblog.local" desc:"sender address"`

	SiteURL        string `env:"SITE_URL" default:"http://localhost:8081" desc:"public client site, for links in email, RSS and sitemap"`
	ModeratorIPs   string `env:"ACCESS_MODERATOR_IPS" desc:"comma-separated client IPs moderators may use the API from; empty = any"`
	ModeratorHours string `env:"ACCESS_MODERATOR_HOURS" desc:"HH:MM-HH:MM window (ACCESS_TIMEZONE) moderators may use the API in; empty = any time"`
	AccessTimezone string `env:"ACCESS_TIMEZONE" default:"Asia/Tashkent" desc:"time zone of env.time / env.weekday in access policies"`
	TrustProxy     bool   `env:"TRUST_PROXY" default:"false" desc:"read client IP from X-Real-IP (set by nginx)"`
	// TrustedProxies limits TRUST_PROXY: X-Real-IP is honoured only from these peers.
	TrustedProxies string `env:"TRUSTED_PROXIES" default:"127.0.0.1/32,::1/128,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16" desc:"comma-separated CIDRs allowed to set X-Real-IP"`
	CORSOrigins    string `env:"CORS_ORIGINS" desc:"comma-separated browser origins allowed by CORS; empty = SITE_URL and http://localhost:8082"`
	Worker         bool   `env:"WORKER" default:"true" desc:"run background jobs in this process"`
	MetricsAddr    string `env:"METRICS_ADDR" default:":9090" desc:"private listener for /metrics; empty disables it"`
	// MetricsToken additionally serves /metrics on the public port, behind
	// "Authorization: Bearer <token>". Empty keeps /metrics off the public port.
	MetricsToken string `env:"METRICS_TOKEN,secret" desc:"bearer token for /metrics on the public port (>= 32 chars); empty = not served there"`

	MigrateOnStart     bool          `env:"MIGRATE_ON_START" default:"true" desc:"apply app + Guard migrations and the access seed at start, under advisory locks (dev convenience); set false in prod and run cmd/migrate as a release job"`
	DBMaxConns         int32         `env:"DB_MAX_CONNS" default:"20" desc:"Postgres pool size"`
	DBMinConns         int32         `env:"DB_MIN_CONNS" default:"2" desc:"Postgres idle connections kept open"`
	DBMaxConnLifetime  time.Duration `env:"DB_MAX_CONN_LIFETIME" default:"30m" desc:"recycle Postgres connections after this"`
	DBStatementTimeout time.Duration `env:"DB_STATEMENT_TIMEOUT" default:"5s" desc:"Postgres statement_timeout; 0 disables"`

	// MFAKey encrypts TOTP secrets at rest. Changing it disables every
	// user's two-factor setup (secrets no longer decrypt).
	MFAKey string `env:"MFA_KEY,secret" desc:"key (>= 32 chars) that encrypts two-factor secrets; generate with: openssl rand -hex 32"`
	// UnsubscribeKey signs one-click unsubscribe links.
	// Empty outside dev falls back to the legacy derivation from MFA_KEY
	// (sha256("unsubscribe:"+MFA_KEY)) so links already emailed keep working;
	// setting it invalidates those links.
	UnsubscribeKey string `env:"UNSUBSCRIBE_KEY,secret" desc:"key (>= 32 chars) that signs unsubscribe links; generate with: openssl rand -hex 32; empty = derived from MFA_KEY (legacy)"`

	// Argon2id password hashing (Guard). Changing these rehashes each
	// password on its next login; don't change them casually in production.
	PasswordHashMemoryKiB uint32 `env:"PASSWORD_HASH_MEMORY_KIB" default:"65536" desc:"argon2id memory in KiB (min 19456)"`
	PasswordHashTime      uint32 `env:"PASSWORD_HASH_TIME" default:"3" desc:"argon2id iterations (min 2)"`
	PasswordHashThreads   uint8  `env:"PASSWORD_HASH_THREADS" default:"2" desc:"argon2id parallelism (min 1)"`
	// AuditEmailKey keys the email fingerprint in Guard's audit events (HMAC
	// instead of plain SHA-256). Generate with: openssl rand -hex 32
	AuditEmailKey string `env:"AUDIT_EMAIL_KEY,secret" desc:"hex key, >= 32 bytes, for email HMAC in audit events"`

	SiteName string `env:"SITE_NAME" default:"iBlog" desc:"product name in feeds and prerendered pages"`

	// Cloudflare Turnstile CAPTCHA on signup. Both empty disables it.
	TurnstileSiteKey string `env:"TURNSTILE_SITE_KEY" desc:"public Turnstile site key sent to the client; empty disables CAPTCHA"`
	TurnstileSecret  string `env:"TURNSTILE_SECRET,secret" desc:"Turnstile secret key that verifies signup tokens"`
	// TurnstileVerifyURL overrides Cloudflare's siteverify endpoint (tests).
	TurnstileVerifyURL string `env:"TURNSTILE_VERIFY_URL" desc:"siteverify endpoint override (tests only)"`

	Analytics bool `env:"ANALYTICS" default:"true" desc:"record product analytics events (POST /api/events)"`

	RequireVerifiedEmail bool `env:"REQUIRE_VERIFIED_EMAIL" default:"true" desc:"block posting, commenting and new publications until the email is confirmed"`

	// ImportAllowPrivate lets story import fetch private addresses (tests only).
	ImportAllowPrivate bool `env:"IMPORT_ALLOW_PRIVATE" default:"false" desc:"allow story import from private and loopback addresses; never in production"`

	// RateLimitMultiplier scales every per-route limit; e2e suites raise it
	// so repeated runs don't hit 429. Production refuses anything but 1.
	RateLimitMultiplier int `env:"RATE_LIMIT_MULTIPLIER" default:"1" desc:"multiply all rate limits (dev only; production requires 1)"`

	// Spector API console (/api/docs/), generated from this repo's source.
	DocsDir        string `env:"DOCS_DIR" default:"." desc:"source directory Spector scans"`
	DocsKey        string `env:"DOCS_KEY,secret" desc:"access key for the console; empty disables the console"`
	DocsProduction bool   `env:"DOCS_PRODUCTION" default:"true" desc:"hide source paths in the console"`
	PublicURL      string `env:"PUBLIC_URL" default:"http://localhost:8080" desc:"base URL the console calls"`
}

// Load reads .env (if present) and the process environment; the process
// environment wins.
func Load() (Config, error) {
	cfg, err := oneenv.Parse[Config]()
	if err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return *cfg, nil
}

// Dev reports whether the app runs in development mode.
func (c Config) Dev() bool { return c.Env == "dev" }

// Validate fills development defaults in dev and refuses missing or
// well-known secrets everywhere else.
func (c *Config) Validate() error {
	if c.Env != "dev" && c.Env != "production" {
		return fmt.Errorf("config: APP_ENV must be dev or production, got %q", c.Env)
	}
	if _, err := c.TrustedProxyPrefixes(); err != nil {
		return err
	}
	if (c.TurnstileSiteKey == "") != (c.TurnstileSecret == "") {
		return errors.New("config: set both TURNSTILE_SITE_KEY and TURNSTILE_SECRET, or neither")
	}
	if c.RateLimitMultiplier < 0 {
		return fmt.Errorf("config: RATE_LIMIT_MULTIPLIER must be >= 1, got %d", c.RateLimitMultiplier)
	}
	c.RateLimitMultiplier = max(c.RateLimitMultiplier, 1)
	if c.Dev() {
		if c.MFAKey == "" {
			c.MFAKey = DevMFAKey
		}
		if c.UnsubscribeKey == "" {
			c.UnsubscribeKey = DevUnsubscribeKey
		}
		return nil
	}
	var errs []error
	strong := func(name, v string) {
		if weakSecrets[v] || len(v) < 32 {
			errs = append(errs, fmt.Errorf("%s must be set to a random value of at least 32 characters", name))
		}
	}
	strong("MFA_KEY", c.MFAKey)
	if c.UnsubscribeKey != "" {
		strong("UNSUBSCRIBE_KEY", c.UnsubscribeKey)
	}
	if c.MetricsToken != "" {
		strong("METRICS_TOKEN", c.MetricsToken)
	}
	if c.AdminPassword != "" && (weakSecrets[c.AdminPassword] || len(c.AdminPassword) < 12) {
		errs = append(errs, errors.New("ADMIN_PASSWORD is a known default or shorter than 12 characters"))
	}
	if c.RateLimitMultiplier > 1 {
		errs = append(errs, errors.New("RATE_LIMIT_MULTIPLIER must be 1"))
	}
	if weakSecrets[c.S3SecretKey] {
		errs = append(errs, errors.New("S3_SECRET_KEY is a known default"))
	}
	if u, err := url.Parse(c.DatabaseURL); err == nil {
		if pw, _ := u.User.Password(); weakSecrets[pw] {
			errs = append(errs, errors.New("DATABASE_URL uses a known default password"))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("config (APP_ENV=production; use APP_ENV=dev locally): %w", errors.Join(errs...))
	}
	return nil
}

// TrustedProxyPrefixes parses TRUSTED_PROXIES.
func (c Config) TrustedProxyPrefixes() ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, s := range splitList(c.TrustedProxies) {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return nil, fmt.Errorf("config: TRUSTED_PROXIES: %w", err)
		}
		out = append(out, p)
	}
	return out, nil
}

// AllowedOrigins is CORS_ORIGINS, or the client site and the local admin.
func (c Config) AllowedOrigins() []string {
	if o := splitList(c.CORSOrigins); len(o) > 0 {
		return o
	}
	return []string{strings.TrimRight(c.SiteURL, "/"), "http://localhost:8082"}
}

func splitList(s string) []string {
	var out []string
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}
