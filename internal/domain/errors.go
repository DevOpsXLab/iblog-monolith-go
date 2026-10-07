// Package domain holds types and errors shared by all bounded contexts.
package domain

import (
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
)

var (
	ErrNotFound     = errors.New("not found")
	ErrConflict     = errors.New("already exists")
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden    = errors.New("forbidden")
	// ErrEmailNotVerified is a forbidden write by a user whose email is not
	// confirmed yet.
	ErrEmailNotVerified = errors.New("email not verified")
)

// ValidationError is returned when input breaks a domain rule.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func Invalid(msg string) error { return &ValidationError{Msg: msg} }

// Paging is a validated page request.
type Paging struct {
	Page  int
	Limit int
	// Cursor, when set, replaces Page with keyset pagination: an opaque
	// next_cursor from the previous page.
	Cursor string
}

const (
	DefaultLimit = 10
	MaxLimit     = 50
	// MaxPage caps offset paging (deep OFFSETs scan and discard every
	// earlier row; use the cursor instead) and keeps Offset from overflowing.
	MaxPage = 1000
)

// NewPaging clamps page and limit to sane values.
func NewPaging(page, limit int) Paging {
	if page < 1 {
		page = 1
	}
	if page > MaxPage {
		page = MaxPage
	}
	if limit < 1 {
		limit = DefaultLimit
	}
	if limit > MaxLimit {
		limit = MaxLimit
	}
	return Paging{Page: page, Limit: limit}
}

// Offset is clamped too, for Paging values built without NewPaging.
func (p Paging) Offset() int {
	page, limit := min(max(p.Page, 1), MaxPage), min(max(p.Limit, 0), MaxLimit)
	return (page - 1) * limit
}

var ErrBadCursor = Invalid("bad cursor")

// EncodeCursor packs keyset values into an opaque page cursor.
func EncodeCursor(parts ...int64) string {
	s := make([]string, len(parts))
	for i, p := range parts {
		s[i] = strconv.FormatInt(p, 10)
	}
	return base64.RawURLEncoding.EncodeToString([]byte(strings.Join(s, ":")))
}

// DecodeCursor unpacks an EncodeCursor value with n parts.
func DecodeCursor(cursor string, n int) ([]int64, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return nil, ErrBadCursor
	}
	fields := strings.Split(string(raw), ":")
	if len(fields) != n {
		return nil, ErrBadCursor
	}
	out := make([]int64, n)
	for i, f := range fields {
		if out[i], err = strconv.ParseInt(f, 10, 64); err != nil || out[i] <= 0 {
			return nil, ErrBadCursor
		}
	}
	return out, nil
}

// Meta describes one page of a list response ({"data": [...], "meta": {...}}).
type Meta struct {
	Page  int `json:"page,omitempty"` // 0 with a cursor
	Limit int `json:"limit"`
	// Total counts all items; with a cursor, the items from the cursor on.
	Total      int    `json:"total"`
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
}

// NewMeta builds page metadata; more pages exist when there is a next
// cursor or offset paging has not reached total.
func NewMeta(page, limit, total int, next string) Meta {
	return Meta{Page: page, Limit: limit, Total: total, NextCursor: next,
		HasMore: next != "" || page > 0 && page*limit < total}
}

// Paginated is a page of items with its metadata; the HTTP layer sends it
// as {"data": items, "meta": meta}.
type Paginated interface {
	Paginated() (items any, meta any)
}
