// SPDX-License-Identifier: Apache-2.0
package adminauth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// A fixed memory/parallelism envelope avoids accepting attacker-sized parameters.
// Iterations are calibrated at install time; changing them needs a migration plan.
const PasswordMemoryKiB = 64 * 1024
const MaxPasswordBytes = 1024

var passwordSlots = make(chan struct{}, 2) // Per process, shared by all hasher instances.

type PasswordHash struct{ encoded string }

func (PasswordHash) String() string               { return "[REDACTED_PASSWORD_HASH]" }
func (PasswordHash) GoString() string             { return "[REDACTED_PASSWORD_HASH]" }
func (PasswordHash) MarshalJSON() ([]byte, error) { return json.Marshal("[REDACTED_PASSWORD_HASH]") }
func (h PasswordHash) StorageValue() string       { return h.encoded }

type PasswordHasher struct {
	iterations uint32
	dummy      PasswordHash
	source     func(context.Context) (uint32, error)
	mu         *sync.Mutex
	current    *PasswordHasher
}

// NewPasswordHasherSource reads the installation's durable parameters for every
// operation. Static lab hashers keep their existing behavior. No credential is
// reinterpreted or silently migrated when an installation is upgraded.
func NewPasswordHasherSource(source func(context.Context) (uint32, error)) *PasswordHasher {
	return &PasswordHasher{source: source, mu: &sync.Mutex{}}
}

func (h *PasswordHasher) configured(ctx context.Context) (*PasswordHasher, error) {
	n, err := h.source(ctx)
	if err != nil || n < 2 || n > 10 {
		return nil, ErrAuthUnavailable
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.current == nil || h.current.iterations != n {
		h.current, err = NewPasswordHasher(ctx, n)
	}
	return h.current, err
}

func (PasswordHasher) String() string   { return "[REDACTED_PASSWORD_HASHER]" }
func (PasswordHasher) GoString() string { return "[REDACTED_PASSWORD_HASHER]" }
func (PasswordHasher) MarshalJSON() ([]byte, error) {
	return json.Marshal("[REDACTED_PASSWORD_HASHER]")
}

func NewPasswordHasher(ctx context.Context, iterations uint32) (*PasswordHasher, error) {
	if iterations < 2 || iterations > 10 {
		return nil, ErrAuthUnavailable
	}
	h := &PasswordHasher{iterations: iterations}
	random, _, err := NewCSRFToken()
	if err != nil {
		return nil, ErrAuthUnavailable
	}
	h.dummy, err = h.Hash(ctx, random)
	if err != nil {
		return nil, err
	}
	return h, nil
}
func ValidLoginPassword(password string) bool {
	return len(password) > 0 && len(password) <= MaxPasswordBytes && utf8.ValidString(password)
}
func derivePassword(ctx context.Context, password string, salt []byte, iterations uint32) ([]byte, error) {
	if ctx.Err() != nil {
		return nil, ErrAuthUnavailable
	}
	select {
	case passwordSlots <- struct{}{}:
	default:
		return nil, ErrAuthUnavailable
	}
	defer func() { <-passwordSlots }()
	// Argon2 cannot be canceled mid-derivation; work and memory are bounded.
	result := argon2.IDKey([]byte(password), salt, iterations, PasswordMemoryKiB, 1, 32)
	if ctx.Err() != nil {
		clear(result)
		return nil, ErrAuthUnavailable
	}
	return result, nil
}
func passwordEncoding(iterations uint32, salt, hash []byte) string {
	return fmt.Sprintf("wrp1$argon2id$v=19$m=65536,t=%d,p=1$%s$%s", iterations,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(hash))
}
func (h *PasswordHasher) Hash(ctx context.Context, password string) (PasswordHash, error) {
	if h != nil && h.source != nil {
		configured, err := h.configured(ctx)
		if err != nil {
			return PasswordHash{}, err
		}
		return configured.Hash(ctx, password)
	}
	// Creation policy only; verification never truncates or normalizes a password.
	if h == nil || h.iterations < 2 || h.iterations > 10 {
		return PasswordHash{}, ErrAuthUnavailable
	}
	if !ValidLoginPassword(password) || utf8.RuneCountInString(password) < 15 {
		return PasswordHash{}, ErrUnauthenticated
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return PasswordHash{}, ErrAuthUnavailable
	}
	key, err := derivePassword(ctx, password, salt, h.iterations)
	if err != nil {
		return PasswordHash{}, err
	}
	defer clear(key)
	return PasswordHash{passwordEncoding(h.iterations, salt, key)}, nil
}
func parsePassword(value string) (uint32, []byte, []byte, bool) {
	if len(value) > 160 {
		return 0, nil, nil, false
	}
	parts := strings.Split(value, "$")
	if len(parts) != 6 || parts[0] != "wrp1" || parts[1] != "argon2id" || parts[2] != "v=19" {
		return 0, nil, nil, false
	}
	n := strings.TrimSuffix(strings.TrimPrefix(parts[3], "m=65536,t="), ",p=1")
	parsed, err := strconv.ParseUint(n, 10, 32)
	if err != nil || parsed < 2 || parsed > 10 {
		return 0, nil, nil, false
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil || len(salt) != 16 {
		return 0, nil, nil, false
	}
	key, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil || len(key) != 32 {
		return 0, nil, nil, false
	}
	if passwordEncoding(uint32(parsed), salt, key) != value {
		return 0, nil, nil, false
	}
	return uint32(parsed), salt, key, true
}

// Verify performs a dummy derivation for absent/invalid records. All active hashes
// must use the installation's configured parameters to avoid cost-based enumeration.
func (h *PasswordHasher) Verify(ctx context.Context, password, stored string) (bool, error) {
	if h != nil && h.source != nil {
		configured, err := h.configured(ctx)
		if err != nil {
			return false, err
		}
		return configured.Verify(ctx, password, stored)
	}
	if h == nil || h.iterations < 2 || h.iterations > 10 || h.dummy.encoded == "" {
		return false, ErrAuthUnavailable
	}
	if !ValidLoginPassword(password) {
		return false, ErrUnauthenticated
	}
	iterations, salt, key, valid := parsePassword(stored)
	valid = valid && iterations == h.iterations
	if !valid {
		iterations, salt, key, _ = parsePassword(h.dummy.encoded)
	}
	actual, err := derivePassword(ctx, password, salt, iterations)
	if err != nil {
		return false, err
	}
	defer clear(actual)
	equal := subtle.ConstantTimeCompare(actual, key) == 1
	if stored != "" && !valid {
		return false, ErrAuthUnavailable
	}
	return valid && equal, nil
}
