package guardauth

import (
	"encoding/hex"
	"fmt"

	"github.com/bakhod1r/guard"
)

// Options tune Guard's password hashing and audit privacy.
type Options struct {
	// Argon2id parameters (memory in KiB). All zero keeps Guard's defaults
	// (64 MiB, 3, 2). Changing them rehashes each password on its next login.
	HashMemoryKiB uint32
	HashTime      uint32
	HashThreads   uint8
	// AuditEmailKey is AUDIT_EMAIL_KEY: hex, at least 32 bytes once decoded.
	// Empty leaves audit events with an unkeyed email fingerprint.
	AuditEmailKey string
	// Migrate applies Guard's migrations and seeds permissions, roles and
	// policies (Seed, SeedAccessRules) when Open runs. MIGRATE_ON_START.
	Migrate bool
	// AccessRules are the config-driven moderator gates seeded with Migrate.
	AccessRules AccessRules
}

// Argon2id floor Guard enforces (OWASP minimum).
const (
	minHashMemoryKiB = 19456
	minHashTime      = 2
	minHashThreads   = 1
	minAuditEmailKey = 32
	hashKeyLen       = 32
	hashSaltLen      = 16
)

// hashParams returns Guard's argon2id override, nil for defaults, or an
// error naming the env vars when the values are below the floor.
func (o Options) hashParams() (*guard.PasswordHashParams, error) {
	if o.HashMemoryKiB == 0 && o.HashTime == 0 && o.HashThreads == 0 {
		return nil, nil
	}
	if o.HashMemoryKiB < minHashMemoryKiB || o.HashTime < minHashTime || o.HashThreads < minHashThreads {
		return nil, fmt.Errorf("PASSWORD_HASH_MEMORY_KIB/TIME/THREADS = %d/%d/%d: below the argon2id minimum %d/%d/%d",
			o.HashMemoryKiB, o.HashTime, o.HashThreads, minHashMemoryKiB, minHashTime, minHashThreads)
	}
	return &guard.PasswordHashParams{
		Memory: o.HashMemoryKiB, Time: o.HashTime, Threads: o.HashThreads,
		KeyLen: hashKeyLen, SaltLen: hashSaltLen,
	}, nil
}

// auditEmailKey decodes AUDIT_EMAIL_KEY; empty returns nil.
func (o Options) auditEmailKey() ([]byte, error) {
	if o.AuditEmailKey == "" {
		return nil, nil
	}
	key, err := hex.DecodeString(o.AuditEmailKey)
	if err != nil {
		return nil, fmt.Errorf("AUDIT_EMAIL_KEY: not hex: %w", err)
	}
	if len(key) < minAuditEmailKey {
		return nil, fmt.Errorf("AUDIT_EMAIL_KEY: %d bytes, need at least %d (openssl rand -hex 32)", len(key), minAuditEmailKey)
	}
	return key, nil
}
