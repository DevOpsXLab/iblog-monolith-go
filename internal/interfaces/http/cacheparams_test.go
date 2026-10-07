package http

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// Every query parameter a handler reads must be known to the HTTP cache,
// or requests using it would bypass the cache (safe, but slow).
func TestCacheKnowsQueryParams(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	re := regexp.MustCompile(`Query\(\)\.(?:Get|Has)\("([a-z_]+)"\)`)
	// Read only by uncached or non-GET routes.
	skip := map[string]bool{"ticket": true, "s": true, "a": true, "sig": true}
	cacheSrc, err := os.ReadFile("middleware/cache.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		src, _ := os.ReadFile(f)
		for _, m := range re.FindAllSubmatch(src, -1) {
			name := string(m[1])
			if !skip[name] && !regexp.MustCompile(`"`+name+`": true`).Match(cacheSrc) {
				t.Errorf("%s reads ?%s; add it to cacheQueryParams in middleware/cache.go", f, name)
			}
		}
	}
}
