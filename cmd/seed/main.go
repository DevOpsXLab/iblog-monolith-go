// Command seed fills a running stack with realistic demo content: authors,
// categories, labels, publications, series, posts, comments, claps,
// follows, bookmarks, reading lists, reads and views.
//
//	go run ./cmd/seed            # against docker compose (API :8080, Postgres :5433, Redis :6380)
//	go run ./cmd/seed -clean     # also delete e2e/test leftovers first
//
// Content goes through the public API, so validation, slugs, reading time,
// revisions and notifications behave as in real use. Postgres is used only
// to mark seed emails verified and to spread timestamps over past months;
// Redis only to record views. Re-running skips what already exists.
package main

import (
	"bufio"
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"math/rand/v2"
	"net/http"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

//go:embed data/posts/*.md
var postFiles embed.FS

var (
	apiURL   = flag.String("api", env("SEED_API", "http://localhost:8080"), "API base URL")
	dbURL    = flag.String("db", env("SEED_DATABASE_URL", "postgres://blog:blog@localhost:5433/blog?sslmode=disable"), "Postgres URL")
	redisURL = flag.String("redis", env("SEED_REDIS_URL", "redis://localhost:6380/0"), "Redis URL")
	adminLog = flag.String("admin", env("ADMIN_USERNAME", "admin"), "admin login")
	adminPw  = flag.String("admin-password", env("ADMIN_PASSWORD", "admin12345"), "admin password")
	password = flag.String("password", env("SEED_PASSWORD", "DevOpsXLab2026"), "password for every seeded user")
	domain   = flag.String("email-domain", "devopsxlab.dev", "email domain for seeded users")
	clean    = flag.Bool("clean", false, "delete e2e/test users, posts and categories first")
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	flag.Parse()
	if err := run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		os.Exit(1)
	}
}

type seedPost struct {
	Key, Title, Subtitle, Author, Category, Publication, Series, Cover, Body string
	Tags, Labels                                                             []string
	DaysAgo                                                                  int
	ID                                                                       int
}

func run(ctx context.Context) error {
	db, err := pgxpool.New(ctx, *dbURL)
	if err != nil {
		return err
	}
	defer db.Close()
	ropt, err := redis.ParseURL(*redisURL)
	if err != nil {
		return err
	}
	rdb := redis.NewClient(ropt)
	defer rdb.Close()

	posts, err := loadPosts()
	if err != nil {
		return err
	}
	if *clean {
		if err := cleanup(ctx, db); err != nil {
			return fmt.Errorf("clean: %w", err)
		}
	}

	admin, err := login(*adminLog, *adminPw)
	if err != nil {
		return fmt.Errorf("admin login: %w", err)
	}
	catIDs, err := ensureCategories(admin)
	if err != nil {
		return err
	}
	labelIDs, err := ensureLabels(admin)
	if err != nil {
		return err
	}

	// Accounts
	users := map[string]*client{}
	for _, p := range people {
		c, err := ensureUser(ctx, db, p)
		if err != nil {
			return fmt.Errorf("user %s: %w", p.Username, err)
		}
		users[p.Username] = c
	}
	step("users", len(users))

	// Publications
	pubIDs := map[string]int{}
	for _, p := range publications {
		owner := users[p.Owner]
		var out struct{ ID int }
		err := owner.do("POST", "/api/publications", map[string]string{
			"slug": p.Slug, "name": p.Name, "description": p.Description,
			"avatar_url": "https://api.dicebear.com/9.x/shapes/svg?seed=" + p.Slug,
		}, &out)
		if isStatus(err, http.StatusConflict) {
			err = owner.do("GET", "/api/publications/"+p.Slug, nil, &out)
		}
		if err != nil {
			return fmt.Errorf("publication %s: %w", p.Slug, err)
		}
		pubIDs[p.Slug] = out.ID
		for u, role := range p.Members {
			if err := owner.do("PUT", "/api/publications/"+p.Slug+"/members/"+u, map[string]string{"role": role}, nil); err != nil {
				return fmt.Errorf("member %s: %w", u, err)
			}
		}
	}
	step("publications", len(pubIDs))

	// Posts, oldest first so feeds and notifications read naturally.
	sort.Slice(posts, func(i, j int) bool { return posts[i].DaysAgo > posts[j].DaysAgo })
	byKey := map[string]*seedPost{}
	created := 0
	for _, p := range posts {
		byKey[p.Key] = p
		author := users[p.Author]
		if author == nil {
			return fmt.Errorf("post %s: unknown author %q", p.Key, p.Author)
		}
		err := db.QueryRow(ctx, `SELECT id FROM posts WHERE user_id = $1 AND title = $2`, author.id, p.Title).Scan(&p.ID)
		if err == nil {
			continue
		}
		draft := map[string]any{
			"title": p.Title, "subtitle": p.Subtitle, "body": p.Body, "tags": p.Tags,
			"status": "published", "cover_url": "https://picsum.photos/seed/" + p.Cover + "/1200/630",
		}
		if id, ok := catIDs[strings.ToLower(p.Category)]; ok {
			draft["category_id"] = id
		}
		var lids []int
		for _, l := range p.Labels {
			if id, ok := labelIDs[l]; ok {
				lids = append(lids, id)
			}
		}
		draft["label_ids"] = lids
		if p.Publication != "" {
			draft["publication_id"] = pubIDs[p.Publication]
		}
		var out struct{ ID int }
		if err := author.do("POST", "/api/posts", draft, &out); err != nil {
			return fmt.Errorf("post %s: %w", p.Key, err)
		}
		p.ID = out.ID
		created++
	}
	step("posts created", created)

	// Series
	for _, s := range seriesDefs {
		owner := users[s.Owner]
		var exists bool
		db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM series WHERE user_id = $1 AND title = $2)`, owner.id, s.Title).Scan(&exists)
		if exists {
			continue
		}
		var out struct{ Slug string }
		if err := owner.do("POST", "/api/series", map[string]string{"title": s.Title, "description": s.Description}, &out); err != nil {
			return fmt.Errorf("series %s: %w", s.Key, err)
		}
		var ids []int
		for _, p := range posts { // already oldest first = reading order
			if p.Series == s.Key {
				ids = append(ids, p.ID)
			}
		}
		if err := owner.do("PUT", "/api/series/"+out.Slug+"/posts", map[string]any{"post_ids": ids}, nil); err != nil {
			return fmt.Errorf("series %s posts: %w", s.Key, err)
		}
	}
	step("series", len(seriesDefs))

	// Social graph
	n := 0
	for who, targets := range follows {
		for _, t := range targets {
			if err := users[who].do("POST", "/api/users/"+t+"/follow", nil, nil); err != nil {
				return fmt.Errorf("follow %s->%s: %w", who, t, err)
			}
			n++
		}
	}
	for _, p := range people {
		for _, t := range p.Tags {
			if err := users[p.Username].do("POST", "/api/tags/"+t+"/follow", nil, nil); err != nil {
				return fmt.Errorf("tag follow: %w", err)
			}
		}
	}
	step("follows", n)

	// Comments: only on posts that have none yet, so re-runs don't duplicate.
	n = 0
	commentIDs := map[string][]int{}
	skip := map[string]bool{}
	for _, p := range posts {
		var cnt int
		db.QueryRow(ctx, `SELECT count(*) FROM comments WHERE post_id = $1`, p.ID).Scan(&cnt)
		skip[p.Key] = cnt > 0
	}
	for _, c := range comments {
		if skip[c.Post] {
			continue
		}
		p := byKey[c.Post]
		body := map[string]any{"text": c.Text}
		if c.Reply > 0 {
			body["parent_id"] = commentIDs[c.Post][c.Reply-1]
		}
		var out struct{ ID int }
		if err := users[c.Author].do("POST", fmt.Sprintf("/api/posts/%d/comments", p.ID), body, &out); err != nil {
			return fmt.Errorf("comment on %s: %w", c.Post, err)
		}
		commentIDs[c.Post] = append(commentIDs[c.Post], out.ID)
		n++
	}
	step("comments", n)

	// Comment likes: post authors like the comments on their posts.
	for key, ids := range commentIDs {
		author := users[byKey[key].Author]
		for _, id := range ids {
			if rand.IntN(3) > 0 {
				author.do("POST", fmt.Sprintf("/api/comments/%d/like", id), nil, nil)
			}
		}
	}

	// Claps and bookmarks: every reader claps for some posts, more for popular topics.
	n = 0
	for _, p := range posts {
		var clapped bool
		db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM likes WHERE post_id = $1)`, p.ID).Scan(&clapped)
		if clapped {
			continue // seeded before
		}
		for _, u := range people {
			if u.Username == p.Author || rand.Float64() > 0.55 {
				continue
			}
			claps := 1 + rand.IntN(12)
			if rand.Float64() < 0.15 {
				claps = 20 + rand.IntN(31)
			}
			if err := users[u.Username].do("POST", fmt.Sprintf("/api/posts/%d/clap", p.ID), map[string]int{"count": claps}, nil); err != nil {
				return fmt.Errorf("clap: %w", err)
			}
			n++
			if rand.Float64() < 0.3 {
				users[u.Username].do("POST", fmt.Sprintf("/api/posts/%d/bookmark", p.ID), nil, nil)
			}
		}
	}
	step("clappers", n)

	// Reading lists
	for _, l := range lists {
		owner := users[l.Owner]
		var exists bool
		db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM lists WHERE user_id = $1 AND name = $2)`, owner.id, l.Name).Scan(&exists)
		if exists {
			continue
		}
		var out struct{ Slug string }
		err := owner.do("POST", "/api/lists", map[string]any{"name": l.Name, "description": l.Description}, &out)
		if isStatus(err, http.StatusConflict) {
			continue
		}
		if err != nil {
			return fmt.Errorf("list %s: %w", l.Name, err)
		}
		for _, k := range l.Posts {
			owner.do("PUT", fmt.Sprintf("/api/lists/%s/posts/%d", out.Slug, byKey[k].ID), nil, nil)
		}
	}
	for u, k := range pins {
		users[u].do("PUT", "/api/me/pin", map[string]int{"post_id": byKey[k].ID}, nil)
	}
	step("lists", len(lists))

	if err := backdate(ctx, db, posts); err != nil {
		return fmt.Errorf("backdate: %w", err)
	}
	if err := views(ctx, db, rdb, posts); err != nil {
		return fmt.Errorf("views: %w", err)
	}
	fmt.Printf("done. log in as any of %s with password %q\n", usernames(), *password)
	return nil
}

func usernames() string {
	var s []string
	for _, p := range people {
		s = append(s, p.Username)
	}
	return strings.Join(s, ", ")
}

func step(what string, n int) { fmt.Printf("%-14s %d\n", what, n) }

// loadPosts parses data/posts/*.md: a "---" front matter block of
// "key: value" lines, then the Markdown body.
func loadPosts() ([]*seedPost, error) {
	files, err := fs.Glob(postFiles, "data/posts/*.md")
	if err != nil {
		return nil, err
	}
	var out []*seedPost
	for _, f := range files {
		raw, err := postFiles.ReadFile(f)
		if err != nil {
			return nil, err
		}
		head, body, ok := strings.Cut(strings.TrimPrefix(string(raw), "---\n"), "\n---\n")
		if !ok {
			return nil, fmt.Errorf("%s: no front matter", f)
		}
		p := &seedPost{Key: strings.SplitN(path.Base(f), "-", 2)[0], Body: strings.TrimSpace(body)}
		sc := bufio.NewScanner(strings.NewReader(head))
		for sc.Scan() {
			k, v, _ := strings.Cut(sc.Text(), ":")
			v = strings.TrimSpace(v)
			switch strings.TrimSpace(k) {
			case "title":
				p.Title = v
			case "subtitle":
				p.Subtitle = v
			case "author":
				p.Author = v
			case "category":
				p.Category = v
			case "tags":
				p.Tags = splitList(v)
			case "labels":
				p.Labels = splitList(v)
			case "publication":
				p.Publication = v
			case "series":
				p.Series = v
			case "days_ago":
				p.DaysAgo, _ = strconv.Atoi(v)
			case "cover":
				p.Cover = v
			}
		}
		out = append(out, p)
	}
	return out, nil
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

func cleanup(ctx context.Context, db *pgxpool.Pool) error {
	tag, err := db.Exec(ctx, `
		DELETE FROM posts
		WHERE title ~ '^(E2E story|Reported |QA probe|Admin post|Alining posti)'
		   OR user_id IN (SELECT id FROM users WHERE username ~ '^(e2e_|rep_|dbg)')`)
	if err != nil {
		return err
	}
	fmt.Printf("%-14s %d\n", "clean posts", tag.RowsAffected())
	tag, err = db.Exec(ctx, `DELETE FROM users WHERE username ~ '^(e2e_|rep_|dbg)'`)
	if err != nil {
		return err
	}
	fmt.Printf("%-14s %d\n", "clean users", tag.RowsAffected())
	_, err = db.Exec(ctx, `DELETE FROM categories WHERE name LIKE 'E2E %'`)
	return err
}

func ensureCategories(admin *client) (map[string]int, error) {
	ids := map[string]int{}
	var have []struct {
		ID   int
		Name string
	}
	if err := admin.do("GET", "/api/categories", nil, &have); err != nil {
		return nil, err
	}
	for _, c := range have {
		ids[strings.ToLower(c.Name)] = c.ID
	}
	for _, name := range categories {
		if _, ok := ids[strings.ToLower(name)]; ok {
			continue
		}
		var out struct{ ID int }
		if err := admin.do("POST", "/api/categories", map[string]string{"name": name}, &out); err != nil {
			return nil, fmt.Errorf("category %s: %w", name, err)
		}
		ids[strings.ToLower(name)] = out.ID
	}
	step("categories", len(categories))
	return ids, nil
}

func ensureLabels(admin *client) (map[string]int, error) {
	ids := map[string]int{}
	var have []struct {
		ID   int
		Name string
	}
	if err := admin.do("GET", "/api/labels", nil, &have); err != nil {
		return nil, err
	}
	for _, l := range have {
		ids[l.Name] = l.ID
	}
	for _, l := range labels {
		if _, ok := ids[l.Name]; ok {
			continue
		}
		var out struct{ ID int }
		if err := admin.do("POST", "/api/labels", map[string]string{"name": l.Name, "color": l.Color}, &out); err != nil {
			return nil, fmt.Errorf("label %s: %w", l.Name, err)
		}
		ids[l.Name] = out.ID
	}
	step("labels", len(labels))
	return ids, nil
}

func ensureUser(ctx context.Context, db *pgxpool.Pool, p person) (*client, error) {
	email := p.Username + "@" + *domain
	c := &client{}
	var sess session
	err := c.do("POST", "/api/auth/register", map[string]string{"username": p.Username, "email": email, "password": *password}, &sess)
	if isStatus(err, http.StatusConflict) {
		return login(p.Username, *password)
	}
	if err != nil {
		return nil, err
	}
	c.token, c.id = sess.Token, sess.User.ID
	// Seed mailboxes are not real; mark them verified so the account can post.
	if _, err := db.Exec(ctx, `UPDATE users SET email_verified_at = now() WHERE id = $1 AND email_verified_at IS NULL`, c.id); err != nil {
		return nil, err
	}
	avatar := p.Avatar
	if avatar == "" {
		avatar = "https://i.pravatar.cc/300?u=" + p.Username + "@devopsxlab"
	}
	err = c.do("PATCH", "/api/me", map[string]string{"display_name": p.Name, "bio": p.Bio, "avatar_url": avatar}, nil)
	return c, err
}

// backdate spreads posts over their days_ago and puts every reaction after
// its post, so feeds, trending and stats look like months of real use.
func backdate(ctx context.Context, db *pgxpool.Pool, posts []*seedPost) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, p := range posts {
		at := time.Now().Add(-time.Duration(p.DaysAgo)*24*time.Hour + time.Duration(8+rand.IntN(10))*time.Hour - 24*time.Hour)
		at = at.Truncate(time.Minute)
		age := fmt.Sprintf("%d seconds", int(time.Since(at).Seconds()))
		stmts := []struct {
			q    string
			args []any
		}{
			{`UPDATE posts SET created_at = $2, published_at = $2, updated_at = $2 WHERE id = $1`, []any{p.ID, at}},
			{`UPDATE post_revisions SET created_at = $2 WHERE post_id = $1`, []any{p.ID, at}},
			// Reactions land between the post and now, front-loaded like real traffic.
			{`UPDATE comments SET created_at = $2::timestamptz + (random() ^ 2) * $3::interval WHERE post_id = $1 AND parent_id IS NULL`, []any{p.ID, at, age}},
			{`UPDATE likes SET created_at = $2::timestamptz + (random() ^ 3) * $3::interval WHERE post_id = $1`, []any{p.ID, at, age}},
			{`UPDATE bookmarks SET created_at = $2::timestamptz + (random() ^ 3) * $3::interval WHERE post_id = $1`, []any{p.ID, at, age}},
			{`UPDATE list_items SET added_at = $2::timestamptz + random() * $3::interval WHERE post_id = $1`, []any{p.ID, at, age}},
		}
		for _, s := range stmts {
			if _, err := tx.Exec(ctx, s.q, s.args...); err != nil {
				return err
			}
		}
	}
	// Replies follow their parent within a day; order by id keeps chains consistent.
	if _, err := tx.Exec(ctx, `
		UPDATE comments c SET created_at = LEAST(now(), p.created_at + random() * interval '20 hours')
		FROM comments p WHERE c.parent_id = p.id`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE notifications n SET created_at = COALESCE(
			(SELECT created_at FROM comments WHERE id = n.comment_id),
			(SELECT published_at + (random() ^ 2) * (now() - published_at) FROM posts WHERE id = n.post_id),
			n.created_at)`); err != nil {
		return err
	}
	// Old notifications are read; the last week stays unread.
	if _, err := tx.Exec(ctx, `UPDATE notifications SET read_at = created_at + interval '3 hours' WHERE created_at < now() - interval '7 days' AND read_at IS NULL`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE users u SET created_at = COALESCE(
			(SELECT min(created_at) - interval '9 days' FROM posts WHERE user_id = u.id),
			now() - interval '130 days' + random() * interval '60 days')
		WHERE email LIKE '%@' || $1`, *domain); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE follows f SET created_at = GREATEST(a.created_at, b.created_at) + random() * interval '20 days'
		FROM users a, users b WHERE a.id = f.follower_id AND b.id = f.followee_id AND a.email LIKE '%@' || $1`, *domain); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// views records unique views in Redis (HyperLogLog + trending day sets, the
// keys internal/infrastructure/redis/views.go reads) and qualified reads in
// post_reads, older posts and popular topics getting more.
func views(ctx context.Context, db *pgxpool.Pool, rdb *redis.Client, posts []*seedPost) error {
	total := 0
	for _, p := range posts {
		var claps int
		db.QueryRow(ctx, `SELECT claps FROM posts WHERE id = $1`, p.ID).Scan(&claps)
		n := 80 + p.DaysAgo*6 + claps*4 + rand.IntN(300)
		key := "views:post:" + strconv.Itoa(p.ID)
		if c, _ := rdb.PFCount(ctx, key).Result(); c > int64(n/2) {
			continue // seeded before
		}
		viewers := make([]any, n)
		for i := range viewers {
			viewers[i] = fmt.Sprintf("seed:%d:%d", p.ID, i)
		}
		pipe := rdb.Pipeline()
		pipe.PFAdd(ctx, key, viewers...)
		// Trending looks back up to 30 days; spread this post's recent views over them.
		for d := 0; d < 30 && d <= p.DaysAgo; d++ {
			day := "trending:" + time.Now().AddDate(0, 0, -d).UTC().Format("20060102")
			w := float64(n) / float64(4+p.DaysAgo-d) // decays with post age
			pipe.ZIncrBy(ctx, day, w, strconv.Itoa(p.ID))
			pipe.Expire(ctx, day, 31*24*time.Hour)
		}
		if _, err := pipe.Exec(ctx); err != nil {
			return err
		}
		reads := n * (35 + rand.IntN(30)) / 100
		if _, err := db.Exec(ctx, `
			INSERT INTO post_reads (post_id, reader, read_at)
			SELECT $1::int, 'seed:' || $1::int || ':' || g, p.published_at + (random() ^ 2) * (now() - p.published_at)
			FROM generate_series(1, $2) g, posts p WHERE p.id = $1::int
			ON CONFLICT DO NOTHING`, p.ID, reads); err != nil {
			return err
		}
		total += n
	}
	rdb.Del(ctx, "trending:cache:7:10", "trending:cache:30:10")
	step("views", total)
	return nil
}

// HTTP

type session struct {
	Token string `json:"token"`
	User  struct {
		ID int `json:"id"`
	} `json:"user"`
}

type client struct {
	token string
	id    int
}

type httpError struct {
	Status int
	Body   string
}

func (e *httpError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.Status, e.Body) }

func isStatus(err error, code int) bool {
	var he *httpError
	return errors.As(err, &he) && he.Status == code
}

func login(name, pw string) (*client, error) {
	c := &client{}
	var s session
	if err := c.do("POST", "/api/auth/login", map[string]string{"login": name, "password": pw}, &s); err != nil {
		return nil, err
	}
	c.token, c.id = s.Token, s.User.ID
	return c, nil
}

var hc = &http.Client{Timeout: 30 * time.Second}

// do sends a JSON request and decodes the "data" envelope into out. It
// waits out rate limits (429) instead of failing.
func (c *client) do(method, p string, in, out any) error {
	for attempt := 0; ; attempt++ {
		var body io.Reader
		if in != nil {
			b, err := json.Marshal(in)
			if err != nil {
				return err
			}
			body = bytes.NewReader(b)
		}
		req, err := http.NewRequest(method, *apiURL+p, body)
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}
		resp, err := hc.Do(req)
		if err != nil {
			return err
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests && attempt < 20 {
			wait, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
			if wait <= 0 {
				wait = 5
			}
			fmt.Printf("  rate limited on %s %s, waiting %ds\n", method, p, wait)
			time.Sleep(time.Duration(wait) * time.Second)
			continue
		}
		if resp.StatusCode >= 300 {
			return &httpError{resp.StatusCode, strings.TrimSpace(string(raw))}
		}
		if out == nil || len(raw) == 0 {
			return nil
		}
		var env struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(raw, &env); err != nil || env.Data == nil {
			return json.Unmarshal(raw, out)
		}
		return json.Unmarshal(env.Data, out)
	}
}
