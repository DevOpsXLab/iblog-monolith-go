// Package mfa is two-factor authentication: TOTP (RFC 6238) codes from an
// authenticator app and single-use backup codes.
package mfa

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain"
)

const (
	Period  = 30 * time.Second
	Digits  = 6
	Issuer  = "DevOpsXLab"
	Skew    = 1 // steps accepted before and after now (clock drift)
	Backups = 10
)

var (
	ErrBadCode     = domain.Invalid("invalid code")
	ErrNotEnabled  = domain.Invalid("two-factor authentication is not enabled")
	ErrEnabled     = domain.Invalid("two-factor authentication is already enabled")
	ErrNoSetup     = domain.Invalid("start setup first")
	b32            = base32.StdEncoding.WithPadding(base32.NoPadding)
	backupAlphabet = "abcdefghjkmnpqrstuvwxyz23456789" // no 0/o, 1/l/i
)

// NewSecret returns a random 160-bit base32 TOTP secret.
func NewSecret() string {
	b := make([]byte, 20)
	rand.Read(b)
	return b32.EncodeToString(b)
}

// URI is the otpauth:// link authenticator apps scan as a QR code.
func URI(secret, account string) string {
	label := url.PathEscape(Issuer + ":" + account)
	q := url.Values{"secret": {secret}, "issuer": {Issuer}, "digits": {fmt.Sprint(Digits)}, "period": {"30"}}
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// Step is the TOTP time step of t.
func Step(t time.Time) int64 { return t.Unix() / int64(Period/time.Second) }

// Code is the TOTP code of secret at step.
func Code(secret string, step int64) (string, error) {
	key, err := b32.DecodeString(strings.ToUpper(secret))
	if err != nil {
		return "", err
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	m := hmac.New(sha1.New, key)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	n := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", Digits, n%1_000_000), nil
}

// Verify checks code against secret around now and returns the matching
// step; callers reject steps not newer than the last one used (replay).
func Verify(secret, code string, now time.Time) (int64, bool) {
	code = strings.TrimSpace(strings.ReplaceAll(code, " ", ""))
	if len(code) != Digits {
		return 0, false
	}
	cur := Step(now)
	for d := int64(-Skew); d <= Skew; d++ {
		want, err := Code(secret, cur+d)
		if err == nil && subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return cur + d, true
		}
	}
	return 0, false
}

// NewBackupCodes returns Backups readable codes like "k7m2p-x9r4t".
func NewBackupCodes() []string {
	out := make([]string, Backups)
	for i := range out {
		b := make([]byte, 10)
		rand.Read(b)
		for j := range b {
			b[j] = backupAlphabet[int(b[j])%len(backupAlphabet)]
		}
		out[i] = string(b[:5]) + "-" + string(b[5:])
	}
	return out
}

// HashBackupCode normalizes and hashes a backup code for storage.
func HashBackupCode(code string) string {
	c := strings.ToLower(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(code)))
	sum := sha256.Sum256([]byte(c))
	return hex.EncodeToString(sum[:])
}

// IsBackupCode tells a backup code ("xxxxx-xxxxx") from a TOTP code.
func IsBackupCode(code string) bool {
	return len(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(code))) == 10
}

// State is a user's two-factor setup. Secret is encrypted at rest.
type State struct {
	Secret    string
	EnabledAt *time.Time
	LastStep  int64
}

func (s State) Enabled() bool { return s.EnabledAt != nil }

// TwoFactorStatus is what the user sees about their two-factor setup.
type TwoFactorStatus struct {
	Enabled     bool       `json:"enabled"`
	EnabledAt   *time.Time `json:"enabled_at,omitempty"`
	BackupCodes int        `json:"backup_codes_left"`
}

// Setup is returned when setup starts: show URI as a QR code.
type Setup struct {
	Secret string `json:"secret"`
	URI    string `json:"otpauth_url"`
}

// Repository returns domain.ErrNotFound when the user has no setup.
type Repository interface {
	Get(ctx context.Context, userID int) (State, error)
	// SavePending replaces a pending (not enabled) setup.
	SavePending(ctx context.Context, userID int, secret string) error
	// Enable turns the pending setup on, records step and stores backup code
	// hashes, in one transaction.
	Enable(ctx context.Context, userID int, step int64, codeHashes []string) error
	Disable(ctx context.Context, userID int) error
	// UseStep records step as used and reports false if it is not newer than
	// the last one (replayed code).
	UseStep(ctx context.Context, userID int, step int64) (bool, error)
	// UseBackupCode marks the code used and reports whether it was valid.
	UseBackupCode(ctx context.Context, userID int, hash string) (bool, error)
	ReplaceBackupCodes(ctx context.Context, userID int, hashes []string) error
	BackupCodesLeft(ctx context.Context, userID int) (int, error)
}
