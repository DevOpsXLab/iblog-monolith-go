package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/bakhod1r/oneenv"
)

// fileLookuper adds the Docker secrets convention to the process environment:
// when KEY is not set but KEY_FILE is, the value is read from that file
// (e.g. MFA_KEY_FILE=/run/secrets/mfa_key). KEY itself always wins, so plain
// env vars and .env keep working unchanged.
type fileLookuper struct {
	next oneenv.Lookuper
	errs []error
}

func newFileLookuper() *fileLookuper { return &fileLookuper{next: oneenv.OSLookuper{}} }

// Lookup implements oneenv.Lookuper.
func (l *fileLookuper) Lookup(key string) (string, bool) {
	if v, ok := l.next.Lookup(key); ok {
		return v, true
	}
	path, ok := l.next.Lookup(key + "_FILE")
	if !ok || path == "" {
		return "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		// Lookup cannot fail; Load reports it after parsing. Never log the value.
		l.errs = append(l.errs, fmt.Errorf("config: %s_FILE: %w", key, err))
		return "", false
	}
	return strings.TrimRight(string(data), "\r\n"), true
}

// LookupRaw keeps secrets literal: a value read from a file is never expanded.
func (l *fileLookuper) LookupRaw(key string) (string, bool) { return l.Lookup(key) }

func (l *fileLookuper) err() error { return errors.Join(l.errs...) }
