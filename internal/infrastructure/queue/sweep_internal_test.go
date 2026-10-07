package queue

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DevOpsXLab/iblog-monolith-go/internal/application"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain/post"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain/sanction"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain/user"
)

type duePosts struct {
	post.Repository
	ids []int
	err error
}

func (d duePosts) DueScheduled(context.Context, time.Time) ([]int, error) { return d.ids, d.err }

type dueUsers struct {
	user.Repository
	ids    []int
	err    error
	cutoff *time.Time
}

func (d dueUsers) DuePurges(_ context.Context, cutoff time.Time) ([]int, error) {
	*d.cutoff = cutoff
	return d.ids, d.err
}

type dueSanctions struct {
	sanction.Repository
	ids []int
	err error
}

func (d dueSanctions) Expired(context.Context, time.Time) ([]int, error) { return d.ids, d.err }

func TestSweepHasWork(t *testing.T) {
	boom := errors.New("db down")
	cases := []struct {
		name      string
		posts     duePosts
		users     dueUsers
		sanctions sanction.Repository
		want      bool
	}{
		{"idle", duePosts{}, dueUsers{}, dueSanctions{}, false},
		{"idle without sanctions repo", duePosts{}, dueUsers{}, nil, false},
		{"scheduled post due", duePosts{ids: []int{1}}, dueUsers{}, dueSanctions{}, true},
		{"purge due", duePosts{}, dueUsers{ids: []int{2}}, dueSanctions{}, true},
		{"suspension over", duePosts{}, dueUsers{}, dueSanctions{ids: []int{3}}, true},
		{"post lookup fails", duePosts{err: boom}, dueUsers{}, dueSanctions{}, true},
		{"user lookup fails", duePosts{}, dueUsers{err: boom}, dueSanctions{}, true},
		{"sanction lookup fails", duePosts{}, dueUsers{}, dueSanctions{err: boom}, true},
	}
	for _, c := range cases {
		var cutoff time.Time
		c.users.cutoff = &cutoff
		h := Handlers{
			Blog:     application.NewBlog(application.Repos{Posts: c.posts, Users: c.users}, application.Ports{}),
			Accounts: &application.Accounts{Sanctions: c.sanctions},
		}
		if got := sweepHasWork(context.Background(), h); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
		if !cutoff.IsZero() {
			if d := time.Since(cutoff) - user.DeletionGrace; d < 0 || d > time.Minute {
				t.Errorf("%s: purge cutoff off by %v", c.name, d)
			}
		}
	}
}

func TestZapLoggerNeverExits(t *testing.T) {
	l := zapLogger{}
	l.Debug("x")
	l.Info("x")
	l.Warn("x")
	l.Error("x")
	l.Fatal("x") // must return
}
