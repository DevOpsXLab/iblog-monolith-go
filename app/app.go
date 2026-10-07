// Package app is the composition root: it wires configuration,
// infrastructure, application services and HTTP into one runnable App.
package app

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/bakhod1r/errorx"
	"github.com/bakhod1r/guard"
	"github.com/bakhod1r/guard/httpguard"
	"github.com/bakhod1r/guard/ratelimit"
	"github.com/bakhod1r/spector"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.uber.org/zap"

	"github.com/DevOpsXLab/iblog-monolith-go/config"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/application"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/infrastructure/captcha"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/infrastructure/guardauth"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/infrastructure/importer"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/infrastructure/mail"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/infrastructure/markdown"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/infrastructure/postgres"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/infrastructure/queue"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/infrastructure/redis"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/infrastructure/secretbox"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/infrastructure/storage"
	httpapi "github.com/DevOpsXLab/iblog-monolith-go/internal/interfaces/http"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/interfaces/http/middleware"
)

type App struct {
	Handler http.Handler
	// Metrics serves Prometheus metrics; mount it on a private listener.
	Metrics http.Handler
	// Outbox holds sent mail when SMTP is not configured (dev, tests).
	Outbox *mail.Memory

	worker *queue.Worker
	close  []func()
}

// Start runs the background worker (when enabled).
func (a *App) Start() error {
	if a.worker == nil {
		return nil
	}
	return a.worker.Start()
}

// Close stops the worker and releases connections.
func (a *App) Close() {
	if a.worker != nil {
		a.worker.Stop()
	}
	for i := len(a.close) - 1; i >= 0; i-- {
		a.close[i]()
	}
}

func New(ctx context.Context, cfg config.Config) (_ *App, err error) {
	// a is local, not the named result: "return nil, err" must not hide it
	// from the cleanup below.
	a := &App{}
	if err := errorx.ValidateRegistryErr(); err != nil {
		return nil, fmt.Errorf("error codes: %w", err)
	}
	defer func() {
		if err != nil {
			a.Close()
		}
	}()

	db, err := postgres.Connect(ctx, cfg.DatabaseURL, postgres.Options{
		MaxConns:         cfg.DBMaxConns,
		MinConns:         cfg.DBMinConns,
		MaxConnLifetime:  cfg.DBMaxConnLifetime,
		StatementTimeout: cfg.DBStatementTimeout,
		Migrate:          cfg.MigrateOnStart,
	})
	if err != nil {
		return nil, err
	}
	a.close = append(a.close, db.Close)

	rdb, err := redis.Connect(ctx, cfg.RedisURL)
	if err != nil {
		return nil, err
	}
	a.close = append(a.close, func() { rdb.Close() })
	if err := middleware.ResetCache(ctx, rdb); err != nil {
		return nil, err
	}

	g, err := guardauth.Open(ctx, db, rdb, guardauth.Options{
		HashMemoryKiB: cfg.PasswordHashMemoryKiB,
		HashTime:      cfg.PasswordHashTime,
		HashThreads:   cfg.PasswordHashThreads,
		AuditEmailKey: cfg.AuditEmailKey,
	})
	if err != nil {
		return nil, err
	}
	a.close = append(a.close, func() { g.Close(context.Background()) })
	if err := guardauth.Seed(ctx, g); err != nil {
		return nil, fmt.Errorf("seed access: %w", err)
	}
	if err := guardauth.SeedAccessRules(ctx, g, guardauth.AccessRules{
		ModeratorIPs: splitList(cfg.ModeratorIPs), ModeratorHours: cfg.ModeratorHours,
	}); err != nil {
		return nil, fmt.Errorf("access rules: %w", err)
	}

	st, err := storage.NewMinIO(ctx, cfg.S3Endpoint, cfg.S3AccessKey, cfg.S3SecretKey, cfg.S3Bucket, cfg.S3UseSSL)
	if err != nil {
		return nil, fmt.Errorf("minio: %w", err)
	}

	var mailer application.Mailer
	if cfg.SMTPAddr != "" {
		mailer = mail.SMTP{Addr: cfg.SMTPAddr, From: cfg.MailFrom}
	} else {
		a.Outbox = &mail.Memory{}
		mailer = a.Outbox
	}

	jobs := queue.NewClient(rdb)
	a.close = append(a.close, func() { jobs.Close() })

	users := postgres.NewUserRepo(db)
	blog := application.NewBlog(application.Repos{
		Posts:        postgres.NewPostRepo(db),
		Comments:     postgres.NewCommentRepo(db),
		Categories:   postgres.NewCategoryRepo(db),
		Users:        users,
		Social:       postgres.NewSocialRepo(db),
		Reports:      postgres.NewReportRepo(db),
		Publications: postgres.NewPublicationRepo(db),
		Stats:        postgres.NewStatsRepo(db),
		Relations:    postgres.NewRelationRepo(db),
		Revisions:    postgres.NewRevisionRepo(db),
		Series:       postgres.NewSeriesRepo(db),
		Discovery:    postgres.NewDiscoveryRepo(db),
		Lists:        postgres.NewListRepo(db),
	}, application.Ports{
		Authz:    guardauth.Authorizer{G: g},
		Audit:    guardauth.Auditor{G: g},
		Jobs:     jobs,
		Events:   redis.Events{RDB: rdb},
		Views:    redis.Views{RDB: rdb},
		Markdown: markdown.New(),
		Importer: importer.New(cfg.ImportAllowPrivate),
	})
	blog.RequireVerifiedEmail = cfg.RequireVerifiedEmail
	blog.SiteURL = cfg.SiteURL
	blog.APIURL = cfg.PublicURL
	if cfg.MFAKey == "" {
		return nil, fmt.Errorf("config: MFA_KEY is required (set APP_ENV=dev for development keys)")
	}
	unsubSecret := cfg.UnsubscribeKey
	if unsubSecret == "" {
		// Legacy derivation: links emailed before UNSUBSCRIBE_KEY existed
		// were signed with a key derived from MFA_KEY.
		zap.L().Warn("UNSUBSCRIBE_KEY not set: unsubscribe links are signed with a key derived from MFA_KEY")
		unsubSecret = cfg.MFAKey
	}
	unsubKey := sha256.Sum256([]byte("unsubscribe:" + unsubSecret))
	blog.UnsubscribeKey = unsubKey[:]
	accounts := application.NewAccounts(users, guardauth.Identity{G: g},
		redis.ResetTokens{RDB: rdb}, redis.ResetTokens{RDB: rdb, Prefix: "verify"}, blog, cfg.SiteURL)
	if cfg.Dev() {
		zap.L().Warn("APP_ENV=dev: development secrets allowed; never run this in production")
	}
	box, err := secretbox.New(cfg.MFAKey)
	if err != nil {
		return nil, err
	}
	accounts.MFA = postgres.NewMFARepo(db)
	accounts.Challenges = redis.MFAChallenges{RDB: rdb}
	accounts.Secrets = box
	accounts.Sanctions = postgres.NewSanctionRepo(db)

	if cfg.AdminUsername != "" && cfg.AdminEmail != "" && cfg.AdminPassword != "" {
		if err := accounts.EnsureAdmin(ctx, cfg.AdminUsername, cfg.AdminEmail, cfg.AdminPassword); err != nil {
			return nil, fmt.Errorf("seed admin: %w", err)
		}
	}

	var analytics *application.Analytics
	if cfg.Analytics {
		analytics = application.NewAnalytics(postgres.NewAnalyticsRepo(db), guardauth.Authorizer{G: g})
	}
	var verifyCaptcha func(ctx context.Context, token, ip string) error
	if cfg.TurnstileSecret != "" {
		verifyCaptcha = captcha.Turnstile{Secret: cfg.TurnstileSecret, URL: cfg.TurnstileVerifyURL}.Verify
	} else if !cfg.Dev() {
		zap.L().Warn("TURNSTILE_SECRET not set: signup has no CAPTCHA, only rate limits and a honeypot")
	}

	if cfg.Worker {
		a.worker = queue.NewWorker(rdb, queue.Handlers{Blog: blog, Accounts: accounts, Mailer: mailer, Storage: st, Analytics: analytics})
	}

	trusted, err := cfg.TrustedProxyPrefixes()
	if err != nil {
		return nil, err
	}
	ip := middleware.ClientIP(cfg.TrustProxy, trusted)
	loc, err := time.LoadLocation(cfg.AccessTimezone)
	if err != nil {
		return nil, fmt.Errorf("ACCESS_TIMEZONE: %w", err)
	}
	gopts := httpguard.Options{
		AuthPath:  "/api/guard/auth",
		AdminPath: "/api/guard",
		ErrorLogger: func(r *http.Request, err error) {
			zap.L().Error("guard", zap.String("path", r.URL.Path), zap.Error(err))
		},
		ClientIP:     ip,
		MaxBodyBytes: 1 << 20,
	}

	api := http.NewServeMux()
	httpapi.NewHandler(httpapi.Deps{
		Blog:     blog,
		Accounts: accounts,
		Storage:  st,
		Jobs:     jobs,
		Events:   redis.Events{RDB: rdb},
		Limit:    limiter(g, cfg.RateLimitMultiplier),
		UserID:   guardauth.UserID,
		Health: map[string]func(context.Context) error{
			"postgres": db.Ping,
			"redis":    func(ctx context.Context) error { return rdb.Ping(ctx).Err() },
			"minio":    st.Ping,
		},
		SiteURL:  cfg.SiteURL,
		ClientIP: ip,
		Tickets:  redis.ResetTokens{RDB: rdb, Prefix: "sse"},

		Analytics:      analytics,
		Captcha:        verifyCaptcha,
		CaptchaSiteKey: cfg.TurnstileSiteKey,
		SiteName:       cfg.SiteName,
	}).Routes(api)
	// Guard's own API: sessions, API keys, roles, permissions, ABAC policies,
	// user status (ban), audit log.
	httpguard.Mount(api, g, gopts)

	var h http.Handler = withoutGuardLogin(middleware.SpanRoute(api))
	h = middleware.Cache(rdb, h)
	h = guardauth.WithPrincipal(h)
	h = guardauth.AccessGate(g, ip, loc)(h)
	h = httpguard.Authenticate(g, gopts)(h)
	h = middleware.GuardFormat(h) // Guard's responses in the API's format
	h = middleware.CORS(cfg.AllowedOrigins())(h)

	root := http.NewServeMux()
	root.Handle("/", h)
	mountDocs(root, cfg)
	a.Metrics = promhttp.Handler()
	if cfg.MetricsToken != "" {
		root.Handle("GET /metrics", requireBearer(cfg.MetricsToken, a.Metrics))
	}

	a.Handler = otelhttp.NewHandler(middleware.RequestID(middleware.Observe(root)), "http",
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			return r.Method // renamed to the matched route by middleware.SpanRoute
		}),
	)
	return a, nil
}

// requireBearer serves next only to "Authorization: Bearer <token>"
// (constant-time compare); anything else gets 404 so the endpoint is not
// advertised.
func requireBearer(token string, next http.Handler) http.Handler {
	want := sha256.Sum256([]byte(token))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		sum := sha256.Sum256([]byte(got))
		if !ok || subtle.ConstantTimeCompare(sum[:], want[:]) != 1 {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// withoutGuardLogin hides Guard's own password login: it would skip two-factor
// authentication and account restore. Clients use POST /api/auth/login.
func withoutGuardLogin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// PUT /password would change the password without revoking API keys
		// or recording the blog's audit event; clients use PUT /api/me/password.
		if r.URL.Path == "/api/guard/auth/login" || r.URL.Path == "/api/guard/auth/register" ||
			r.URL.Path == "/api/guard/auth/password" {
			errorx.WriteProblemRequest(w, r, errorx.New(errorx.ErrNotFound, "hidden").WithDetails("not found"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// limiter adapts Guard's Redis sliding-window limiter.
func limiter(g *guard.Guard, multiplier int) httpapi.Limiter {
	return limitWith(multiplier, func(ctx context.Context, key string, rule ratelimit.Rule) (bool, time.Duration, error) {
		res, err := g.Limiter.Allow(ctx, key, rule)
		return res.Allowed, res.RetryAfter, err
	})
}

// sensitiveLimits fail closed: without Redis, password guessing must not
// become unlimited. Everything else (likes, views) fails open.
var sensitiveLimits = []string{"login", "register", "forgot", "reset", "mfa", "verify"}

func limitWith(multiplier int, allow func(ctx context.Context, key string, rule ratelimit.Rule) (bool, time.Duration, error)) httpapi.Limiter {
	multiplier = max(multiplier, 1)
	return func(ctx context.Context, key string, n int, window time.Duration) (bool, time.Duration) {
		ok, retry, err := allow(ctx, "blog:"+key, ratelimit.Rule{Limit: n * multiplier, Window: window})
		if err == nil {
			return ok, retry
		}
		zap.L().Warn("rate limit", zap.String("key", key), zap.Error(err))
		for _, p := range sensitiveLimits {
			if strings.HasPrefix(key, p) {
				return false, 30 * time.Second
			}
		}
		return true, 0
	}
}

// mountDocs serves the Spector API console at /api/docs/. Spector builds the
// OpenAPI document from the Go source in cfg.DocsDir when the app starts, so
// the docs always match the code.
func mountDocs(mux *http.ServeMux, cfg config.Config) {
	const base = "/api/docs"
	if !cfg.Dev() && cfg.DocsKey == "" {
		zap.L().Info("API console disabled: set DOCS_KEY to enable it outside APP_ENV=dev")
		return
	}
	docs := spector.Handler(spector.Config{
		Dir:        cfg.DocsDir,
		Title:      "DevOpsXLab Blog API",
		Version:    "2.0.0",
		Adapter:    "stdlib",
		BasePath:   base,
		AccessKey:  cfg.DocsKey,
		Production: cfg.DocsProduction || !cfg.Dev(),
		Servers:    []spector.Server{{URL: cfg.PublicURL}},
		Security: map[string]spector.SecurityScheme{
			"bearerAuth": {Type: "http", Scheme: "bearer", BearerFormat: "opaque session token"},
		},
	})
	mux.HandleFunc(base, func(w http.ResponseWriter, r *http.Request) {
		target := base + "/"
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery // keep ?key= on the first visit
		}
		http.Redirect(w, r, target, http.StatusMovedPermanently)
	})
	mux.Handle(base+"/", http.StripPrefix(base, docs))
}

// splitList splits a comma-separated setting, dropping blanks.
func splitList(s string) []string {
	var out []string
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}
