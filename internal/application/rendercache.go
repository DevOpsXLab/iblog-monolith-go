package application

import (
	"crypto/sha256"
	"sync"
)

// renderCache memoises Markdown rendering by body hash, so readers who
// bypass the HTTP cache (logged in) don't re-render the same post.
type renderCache struct {
	mu    sync.Mutex
	max   int
	items map[[32]byte]string
	order [][32]byte // FIFO eviction; good enough for hot posts
}

func newRenderCache(max int) *renderCache {
	return &renderCache{max: max, items: make(map[[32]byte]string, max)}
}

func (c *renderCache) render(m Markdown, body string) string {
	if c == nil {
		return m.Render(body)
	}
	key := sha256.Sum256([]byte(body))
	c.mu.Lock()
	html, ok := c.items[key]
	c.mu.Unlock()
	if ok {
		return html
	}
	html = m.Render(body)
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.items[key]; !ok {
		if len(c.order) >= c.max {
			delete(c.items, c.order[0])
			c.order = c.order[1:]
		}
		c.order = append(c.order, key)
		c.items[key] = html
	}
	return html
}
