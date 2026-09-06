// SPDX-License-Identifier: Apache-2.0
package adminauth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrInvalidOrReplayed = errors.New("TOTP_INVALID_OR_REPLAYED")
	ErrAuthUnavailable   = errors.New("AUTH_UNAVAILABLE")
)

const totpPeriod = 30
const totpDigits = 6

// Secret is a 160-bit HMAC-SHA1 key. Normal logging/JSON redacts it.
// This is not encrypted storage: persistence needs the Control-only AEAD layer.
type Secret struct {
	key   [20]byte
	valid bool
}

func (Secret) String() string               { return "[REDACTED_TOTP_SECRET]" }
func (Secret) GoString() string             { return "[REDACTED_TOTP_SECRET]" }
func (Secret) MarshalJSON() ([]byte, error) { return json.Marshal("[REDACTED_TOTP_SECRET]") }

func NewSecret() (Secret, error) {
	s := Secret{valid: true}
	if _, err := rand.Read(s.key[:]); err != nil {
		return Secret{}, err
	}
	return s, nil
}

// Use only for local enrollment display or encryption. Never send to an external
// QR service, logs, metrics, or routine API responses.
func (s Secret) ProvisioningBase32() (string, error) {
	if !s.valid {
		return "", ErrInvalidOrReplayed
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(s.key[:]), nil
}
func ParseSecret(value string) (Secret, error) {
	if len(value) != 32 {
		return Secret{}, ErrInvalidOrReplayed
	}
	decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(value))
	if err != nil || len(decoded) != 20 {
		return Secret{}, ErrInvalidOrReplayed
	}
	s := Secret{valid: true}
	copy(s.key[:], decoded)
	return s, nil
}

type CredentialRef struct {
	UserID  string
	Version uint64
}

func (r CredentialRef) valid() bool {
	if len(r.UserID) == 0 || len(r.UserID) > 128 || r.Version == 0 {
		return false
	}
	for _, c := range r.UserID {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// CounterStore must atomically verify the active credential version and accept
// only counter > lastAcceptedCounter. Missing/reset credentials return false.
// An explicit initial-enrollment transaction may instead insert a new credential
// and its first counter together with the verified enrollment/session transition.
// Initial counter is conceptually -1. All Control replicas must share this store.
// Commit before returning true; unknown writes must never be treated as success.
// No in-memory production implementation is provided.
type CounterStore interface {
	ConsumeCounter(context.Context, CredentialRef, uint64) (bool, error)
}

// The only exported code verifier: a successful stateless match is insufficient.
// ref and secret must come from one trusted credential snapshot, not request fields.
// First validate/throttle the password challenge; the store must atomically consume
// the counter with the challenge/session transition before reporting success.
func VerifyAndConsume(ctx context.Context, ref CredentialRef, secret Secret, code string, now time.Time, store CounterStore) error {
	if ctx.Err() != nil || store == nil {
		return ErrAuthUnavailable
	}
	if !ref.valid() {
		return ErrInvalidOrReplayed
	}
	counter, ok := matchCounter(secret, code, now)
	if !ok {
		return ErrInvalidOrReplayed
	}
	accepted, err := store.ConsumeCounter(ctx, ref, counter)
	if err != nil {
		return ErrAuthUnavailable
	}
	if !accepted {
		return ErrInvalidOrReplayed
	}
	return nil
}

func matchCounter(secret Secret, code string, now time.Time) (uint64, bool) {
	if !secret.valid || len(code) != totpDigits || now.Unix() < 0 {
		return 0, false
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	current := uint64(now.Unix() / totpPeriod)
	var matched uint64
	found := false
	for _, delta := range []int64{-1, 0, 1} {
		if current == 0 && delta == -1 {
			continue
		}
		counter := uint64(int64(current) + delta)
		candidate := hotp(secret.key[:], counter, totpDigits)
		if subtle.ConstantTimeCompare([]byte(candidate), []byte(code)) == 1 {
			// Consume the newest step if the same digits match adjacent counters.
			matched, found = counter, true
		}
	}
	return matched, found
}

// RFC 4226 dynamic truncation with RFC 6238's time counter.
// Eight digits exist only for RFC vectors; public verification is always six.
func hotp(key []byte, counter uint64, digits int) string {
	var moving [8]byte
	binary.BigEndian.PutUint64(moving[:], counter)
	mac := hmac.New(sha1.New, key)
	_, _ = mac.Write(moving[:])
	digest := mac.Sum(nil)
	offset := digest[len(digest)-1] & 0x0f
	value := binary.BigEndian.Uint32(digest[offset:offset+4]) & 0x7fffffff
	modulus := uint32(1000000)
	if digits == 8 {
		modulus = 100000000
	}
	return fmt.Sprintf("%0*d", digits, value%modulus)
}
