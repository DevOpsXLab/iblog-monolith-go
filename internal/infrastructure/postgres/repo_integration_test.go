package postgres_test

// Repository queries behind the periodic sweep (scheduled publishing,
// account purges, expired suspensions), against a real Postgres.
// Skipped with -short or without Docker.

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/iBlog/iblog-monolith-go/internal/domain"
	"github.com/iBlog/iblog-monolith-go/internal/domain/comment"
	"github.com/iBlog/iblog-monolith-go/internal/domain/post"
	"github.com/iBlog/iblog-monolith-go/internal/domain/sanction"
	"github.com/iBlog/iblog-monolith-go/internal/infrastructure/postgres"
)

func connect(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test")
	}
	ctx := context.Background()
	pg, err := tcpostgres.Run(ctx, "postgres:18-alpine",
		tcpostgres.WithDatabase("blog"), tcpostgres.WithUsername("blog"), tcpostgres.WithPassword("blog"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").
			WithOccurrence(2).WithStartupTimeout(60*time.Second)))
	if err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
	t.Cleanup(func() { testcontainers.TerminateContainer(pg) })
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	db, err := postgres.Connect(ctx, dsn, postgres.Options{MaxConns: 4, Migrate: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	return db
}

// create stores a draft normalized the way the application does; scheduled
// drafts keep their past publish_at so the sweep can find them.
func create(t *testing.T, posts *postgres.PostRepo, d post.Draft, authorID int, author string) (post.Post, error) {
	t.Helper()
	at := d.PublishAt
	n, err := d.Normalize(time.Now().Add(-24 * time.Hour))
	must(t, err)
	n.PublishAt = at
	return posts.Create(context.Background(), n, authorID, author)
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestSweepQueries(t *testing.T) {
	db := connect(t)
	ctx := context.Background()
	users := postgres.NewUserRepo(db)
	posts := postgres.NewPostRepo(db)
	comments := postgres.NewCommentRepo(db)
	sanctions := postgres.NewSanctionRepo(db)
	now := time.Now().UTC().Truncate(time.Second)

	ali, err := users.Create(ctx, "ali", "ali@example.com")
	must(t, err)
	vali, err := users.Create(ctx, "vali", "vali@example.com")
	must(t, err)
	if _, err := users.Create(ctx, "ALI", "other@example.com"); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("case-insensitive duplicate: %v", err)
	}

	t.Run("scheduled publishing", func(t *testing.T) {
		soon, later := now.Add(time.Minute), now.Add(time.Hour)
		a, err := create(t, posts, post.Draft{Title: "a", Status: post.StatusScheduled, PublishAt: &later}, ali.ID, ali.Username)
		must(t, err)
		b, err := create(t, posts, post.Draft{Title: "b", Status: post.StatusScheduled, PublishAt: &soon}, ali.ID, ali.Username)
		must(t, err)
		_, err = create(t, posts, post.Draft{Title: "c", Status: post.StatusDraft}, ali.ID, ali.Username)
		must(t, err)

		due, err := posts.DueScheduled(ctx, now)
		if err != nil || len(due) != 0 {
			t.Fatalf("due now = %v, %v", due, err)
		}
		due, err = posts.DueScheduled(ctx, later)
		if err != nil || !slices.Equal(due, []int{b.ID, a.ID}) {
			t.Fatalf("due later = %v, want [%d %d] (oldest first)", due, b.ID, a.ID)
		}
		if ok, err := posts.Publish(ctx, a.ID, now); ok || err != nil {
			t.Errorf("published early: %v %v", ok, err)
		}
		if ok, err := posts.Publish(ctx, b.ID, soon); !ok || err != nil {
			t.Fatalf("publish due: %v %v", ok, err)
		}
		if ok, _ := posts.Publish(ctx, b.ID, soon); ok {
			t.Error("published twice")
		}
		p, err := posts.Get(ctx, b.ID, 0)
		if err != nil || p.Status != post.StatusPublished || p.PublishedAt == nil || p.PublishAt != nil {
			t.Errorf("published post = %+v %v", p, err)
		}
	})

	t.Run("account purge", func(t *testing.T) {
		p, err := create(t, posts, post.Draft{Title: "vali's", Status: post.StatusPublished}, vali.ID, vali.Username)
		must(t, err)
		// ali's comment on vali's post goes with the post.
		_, err = comments.Add(ctx, comment.Comment{PostID: p.ID, UserID: ali.ID, Text: "hi"})
		must(t, err)

		deletedAt := now.Add(-48 * time.Hour)
		must(t, users.MarkDeleted(ctx, vali.ID, deletedAt))
		cutoff := now.Add(-72 * time.Hour) // grace not over
		if ids, err := users.DuePurges(ctx, cutoff); err != nil || len(ids) != 0 {
			t.Fatalf("due before grace = %v %v", ids, err)
		}
		if ok, err := users.Purge(ctx, vali.ID, cutoff); ok || err != nil {
			t.Fatalf("purged before grace: %v %v", ok, err)
		}

		// Restore wins over a later purge.
		must(t, users.Restore(ctx, vali.ID))
		if ok, _ := users.Purge(ctx, vali.ID, now); ok {
			t.Fatal("purged a restored account")
		}

		must(t, users.MarkDeleted(ctx, vali.ID, deletedAt))
		if ids, err := users.DuePurges(ctx, now); err != nil || !slices.Equal(ids, []int{vali.ID}) {
			t.Fatalf("due = %v %v", ids, err)
		}
		if ok, err := users.Purge(ctx, vali.ID, now); !ok || err != nil {
			t.Fatalf("purge: %v %v", ok, err)
		}
		if _, err := users.GetByID(ctx, vali.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("user left: %v", err)
		}
		if _, err := posts.Get(ctx, p.ID, 0); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("post left: %v", err)
		}
		// Purging a missing user is not an error.
		if ok, err := users.Purge(ctx, vali.ID, now); ok || err != nil {
			t.Errorf("purge missing: %v %v", ok, err)
		}
	})

	t.Run("expired suspensions", func(t *testing.T) {
		until := now.Add(time.Hour)
		_, err := sanctions.Save(ctx, sanction.Sanction{UserID: ali.ID, Reason: "spam", Until: &until})
		must(t, err)
		if ids, _ := sanctions.Expired(ctx, now); len(ids) != 0 {
			t.Fatalf("expired early: %v", ids)
		}
		if ids, err := sanctions.Expired(ctx, until); err != nil || !slices.Equal(ids, []int{ali.ID}) {
			t.Fatalf("expired = %v %v", ids, err)
		}
		must(t, sanctions.Delete(ctx, ali.ID))
		if err := sanctions.Delete(ctx, ali.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("second delete: %v", err)
		}
		// A permanent ban never expires.
		_, err = sanctions.Save(ctx, sanction.Sanction{UserID: ali.ID, Reason: "abuse"})
		must(t, err)
		if ids, _ := sanctions.Expired(ctx, now.AddDate(100, 0, 0)); len(ids) != 0 {
			t.Errorf("ban expired: %v", ids)
		}
	})
}
