// Package captcha verifies Cloudflare Turnstile tokens.
package captcha

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrFailed is a missing, expired or forged token.
var ErrFailed = errors.New("captcha failed")

const verifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

// Turnstile checks tokens against Cloudflare's siteverify API.
type Turnstile struct {
	Secret string
	// URL overrides the siteverify endpoint (tests).
	URL    string
	Client *http.Client
}

// Verify returns ErrFailed for a bad token and another error when
// Cloudflare can't be reached (callers decide whether to fail closed).
func (t Turnstile) Verify(ctx context.Context, token, remoteIP string) error {
	token = strings.TrimSpace(token)
	if token == "" || len(token) > 2048 {
		return ErrFailed
	}
	form := url.Values{"secret": {t.Secret}, "response": {token}}
	if remoteIP != "" {
		form.Set("remoteip", remoteIP)
	}
	endpoint := t.URL
	if endpoint == "" {
		endpoint = verifyURL
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	c := t.Client
	if c == nil {
		c = http.DefaultClient
	}
	res, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("turnstile: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("turnstile: status %d", res.StatusCode)
	}
	var out struct {
		Success bool `json:"success"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return fmt.Errorf("turnstile: %w", err)
	}
	if !out.Success {
		return ErrFailed
	}
	return nil
}
