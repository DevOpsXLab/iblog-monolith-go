// Package core wires configuration and infrastructure into the application
// services, without HTTP. The API (package app) and the worker both build on
// it; keeping HTTP out means cmd/worker does not link the router, handlers,
// Guard's HTTP layer or the docs console.
package core

import (
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/bakhod1r/errorx"
	"github.com/bakhod1r/guard"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/iBlog/iblog-monolith-go/config"
	"github.com/iBlog/iblog-monolith-go/internal/application"
	"github.com/iBlog/iblog-monolith-go/internal/infrastructure/guardauth"
	"github.com/iBlog/iblog-monolith-go/internal/infrastructure/importer"
	"github.com/iBlog/iblog-monolith-go/internal/infrastructure/mail"
	"github.com/iBlog/iblog-monolith-go/internal/infrastructure/markdown"
	"github.com/iBlog/iblog-monolith-go/internal/infrastructure/postgres"
	"github.com/iBlog/iblog-monolith-go/internal/infrastructure/queue"
	"github.com/iBlog/iblog-monolith-go/internal/infrastructure/redis"
	"github.com/iBlog/iblog-monolith-go/internal/infrastructure/secretbox"
	"github.com/iBlog/iblog-monolith-go/internal/infrastructure/storage"
)

type Services struct {
	DB      *pgxpool.Pool
	RDB     *goredis.Client
	Guard   *guard.Guard
	Storage *storage.MinIO
	Jobs    *queue.Client
	Mailer  application.Mailer
	// Outbox holds sent mail when SMTP is not configured (dev, tests).
	Outbox *mail.Memory

	Blog      *application.Blog
	Accounts  *application.Accounts
	Analytics *application.Analytics // nil unless cfg.Analytics

	close []func()
}

// Close releases connections in reverse order of opening.
func (s *Services) Close() {
	for i := len(s.close) - 1; i >= 0; i-- {
		s.close[i]()
	}
}

// Handlers is what the background worker needs.
func (s *Services) Handlers() queue.Handlers {
	return queue.Handlers{Blog: s.Blog, Accounts: s.Accounts, Mailer: s.Mailer, Storage: s.Storage, Analytics: s.Analytics}
}

func New(ctx context.Context, cfg config.Config) (_ *Services, err error) {
	// s is local, not the named result: "return nil, err" must not hide it
	// from the cleanup below.
	s := &Services{}
	if err := errorx.ValidateRegistryErr(); err != nil {
		return nil, fmt.Errorf("error codes: %w", err)
	}
	defer func() {
		if err != nil {
			s.Close()
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
	s.DB = db
	s.close = append(s.close, db.Close)

	rdb, err := redis.Connect(ctx, cfg.RedisURL)
	if err != nil {
		return nil, err
	}
	s.RDB = rdb
	s.close = append(s.close, func() { rdb.Close() })

	g, err := guardauth.Open(ctx, db, rdb, guardauth.Options{
		HashMemoryKiB: cfg.PasswordHashMemoryKiB,
		HashTime:      cfg.PasswordHashTime,
		HashThreads:   cfg.PasswordHashThreads,
		AuditEmailKey: cfg.AuditEmailKey,
		// Guard schema + access seed run here only with MIGRATE_ON_START;
		// otherwise cmd/migrate owns them.
		Migrate:     cfg.MigrateOnStart,
		AccessRules: guardauth.NewAccessRules(cfg.ModeratorIPs, cfg.ModeratorHours),
	})
	if err != nil {
		return nil, err
	}
	s.Guard = g
	s.close = append(s.close, func() { g.Close(context.Background()) })

	st, err := storage.NewMinIO(ctx, cfg.S3Endpoint, cfg.S3AccessKey, cfg.S3SecretKey, cfg.S3Bucket, cfg.S3UseSSL)
	if err != nil {
		return nil, fmt.Errorf("minio: %w", err)
	}
	s.Storage = st

	if cfg.SMTPAddr != "" {
		s.Mailer = mail.SMTP{Addr: cfg.SMTPAddr, From: cfg.MailFrom}
	} else {
		s.Outbox = &mail.Memory{}
		s.Mailer = s.Outbox
	}

	jobs := queue.NewClient(rdb)
	s.Jobs = jobs
	s.close = append(s.close, func() { jobs.Close() })

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
	s.Blog = blog

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
	s.Accounts = accounts

	if cfg.AdminUsername != "" && cfg.AdminEmail != "" && cfg.AdminPassword != "" {
		if err := accounts.EnsureAdmin(ctx, cfg.AdminUsername, cfg.AdminEmail, cfg.AdminPassword); err != nil {
			return nil, fmt.Errorf("seed admin: %w", err)
		}
	}

	if cfg.Analytics {
		s.Analytics = application.NewAnalytics(postgres.NewAnalyticsRepo(db), guardauth.Authorizer{G: g})
	}
	return s, nil
}
