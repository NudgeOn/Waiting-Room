// SPDX-License-Identifier: Apache-2.0
package adminauth

import (
	"context"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Public, non-secret reference values from RFC 4226 Appendix D / RFC 6238 Appendix B.
const vectorKey = "12345678901234567890"

func vectorSecret(t *testing.T) Secret {
	t.Helper()
	value := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte(vectorKey))
	secret, err := ParseSecret(value)
	if err != nil {
		t.Fatal(err)
	}
	return secret
}
func TestRFC4226HOTPVectors(t *testing.T) {
	for i, want := range []string{"755224", "287082", "359152", "969429", "338314", "254676", "287922", "162583", "399871", "520489"} {
		if got := hotp([]byte(vectorKey), uint64(i), 6); got != want {
			t.Fatalf("counter %d: RFC vector mismatch", i)
		}
	}
}
func TestRFC6238SHA1Vectors(t *testing.T) {
	for _, v := range []struct {
		seconds int64
		want    string
	}{{59, "94287082"}, {1111111109, "07081804"}, {1111111111, "14050471"}, {1234567890, "89005924"}, {2000000000, "69279037"}, {20000000000, "65353130"}} {
		if hotp([]byte(vectorKey), uint64(v.seconds/30), 8) != v.want {
			t.Fatal("RFC TOTP vector mismatch", v.seconds)
		}
		if counter, ok := matchCounter(vectorSecret(t), v.want[2:], time.Unix(v.seconds, 0)); !ok || counter != uint64(v.seconds/30) {
			t.Fatal("six-digit match mismatch")
		}
	}
}
func TestTOTPWindowAndTimeBoundaries(t *testing.T) {
	secret := vectorSecret(t)
	for _, v := range []struct {
		seconds, counter int64
		want             bool
	}{
		{300, 9, true}, {300, 10, true}, {300, 11, true}, {300, 8, false}, {300, 12, false},
		{59, 0, true}, {60, 0, false}, {59, 3, false}, {60, 3, true}, {0, 0, true}, {-1, 0, false},
	} {
		code := hotp([]byte(vectorKey), uint64(v.counter), 6)
		_, ok := matchCounter(secret, code, time.Unix(v.seconds, 0))
		if ok != v.want {
			t.Fatal("time window mismatch", v)
		}
	}
}
func TestSecretGenerationParsingAndRedaction(t *testing.T) {
	first, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	plain, _ := first.ProvisioningBase32()
	next, _ := second.ProvisioningBase32()
	if len(plain) != 32 || plain == next {
		t.Fatal("invalid generated secrets")
	}
	decoded, err := ParseSecret(strings.ToLower(plain))
	if err != nil || decoded != first {
		t.Fatal("secret round trip")
	}
	for _, bad := range []string{"", "short", strings.Repeat("0", 32), plain + "=", plain + "\n"} {
		if _, err := ParseSecret(bad); !errors.Is(err, ErrInvalidOrReplayed) {
			t.Fatal("invalid secret accepted")
		}
	}
	if _, err := (Secret{}).ProvisioningBase32(); err == nil {
		t.Fatal("zero secret accepted")
	}
	raw, _ := json.Marshal(struct{ Secret Secret }{first})
	for _, out := range []string{fmt.Sprint(first), fmt.Sprintf("%+v", first), fmt.Sprintf("%#v", first), string(raw)} {
		if strings.Contains(out, plain) || !strings.Contains(out, "REDACTED") {
			t.Fatal("secret not redacted")
		}
	}
}

// Unit-only store double. This is not the missing PostgreSQL transaction adapter.
type counterFixture struct {
	mu             sync.Mutex
	version        uint64
	last           map[string]uint64
	err            error
	calls          int
	commitThenFail bool
}

func (s *counterFixture) ConsumeCounter(ctx context.Context, ref CredentialRef, counter uint64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if s.err != nil {
		return false, s.err
	}
	if ref.UserID != "user-one" || ref.Version != s.version {
		return false, nil
	}
	if last, ok := s.last[ref.UserID]; ok && counter <= last {
		return false, nil
	}
	s.last[ref.UserID] = counter
	if s.commitThenFail {
		return false, errors.New("unknown commit")
	}
	return true, nil
}
func newCounterFixture() *counterFixture {
	return &counterFixture{version: 1, last: map[string]uint64{}}
}

func TestTOTPConcurrentReplayExactlyOneSuccess(t *testing.T) {
	s := newCounterFixture()
	secret := vectorSecret(t)
	ref := CredentialRef{"user-one", 1}
	now := time.Unix(300, 0)
	code := hotp([]byte(vectorKey), 10, 6)
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := VerifyAndConsume(context.Background(), ref, secret, code, now, s)
			if err == nil {
				accepted.Add(1)
			} else if !errors.Is(err, ErrInvalidOrReplayed) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if accepted.Load() != 1 || s.calls != 64 {
		t.Fatal("replay race", accepted.Load(), s.calls)
	}
	older := hotp([]byte(vectorKey), 9, 6)
	if !errors.Is(VerifyAndConsume(context.Background(), ref, secret, older, now, s), ErrInvalidOrReplayed) {
		t.Fatal("older accepted after newer")
	}
}
func TestTOTPInvalidInputNeverConsumesCounter(t *testing.T) {
	s := newCounterFixture()
	secret := vectorSecret(t)
	ref := CredentialRef{"user-one", 1}
	now := time.Unix(300, 0)
	for _, code := range []string{"", "12345", "1234567", "abcdef", "１２３４５６", " 12345", "12345 ", "94287082"} {
		if !errors.Is(VerifyAndConsume(context.Background(), ref, secret, code, now, s), ErrInvalidOrReplayed) {
			t.Fatal("invalid code accepted")
		}
	}
	validCode := hotp([]byte(vectorKey), 10, 6)
	if VerifyAndConsume(context.Background(), ref, Secret{}, validCode, now, s) == nil {
		t.Fatal("zero secret accepted")
	}
	for _, bad := range []CredentialRef{{}, {"user-one", 0}, {"user one", 1}, {"missing", 1}} {
		if VerifyAndConsume(context.Background(), bad, secret, "invalid", now, s) == nil {
			t.Fatal("invalid reference accepted")
		}
	}
	if s.calls != 0 {
		t.Fatal("invalid input touched counter")
	}
}
func TestTOTPCredentialResetAndStoreFailure(t *testing.T) {
	s := newCounterFixture()
	secret := vectorSecret(t)
	ref := CredentialRef{"user-one", 1}
	now := time.Unix(300, 0)
	code := hotp([]byte(vectorKey), 10, 6)
	s.version = 2
	if !errors.Is(VerifyAndConsume(context.Background(), ref, secret, code, now, s), ErrInvalidOrReplayed) {
		t.Fatal("old enrollment accepted")
	}
	ref.Version = 2
	s.err = errors.New("private database credential must not leak")
	if err := VerifyAndConsume(context.Background(), ref, secret, code, now, s); err != ErrAuthUnavailable {
		t.Fatal("storage error not sanitized")
	}
	s.err = nil
	s.commitThenFail = true
	if VerifyAndConsume(context.Background(), ref, secret, code, now, s) != ErrAuthUnavailable {
		t.Fatal("unknown write became success")
	}
	s.commitThenFail = false
	if VerifyAndConsume(context.Background(), ref, secret, code, now, s) != ErrInvalidOrReplayed {
		t.Fatal("unknown write replay accepted")
	}
	if VerifyAndConsume(context.Background(), ref, secret, code, now, nil) != ErrAuthUnavailable {
		t.Fatal("nil store accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if VerifyAndConsume(ctx, ref, secret, code, now, s) != ErrAuthUnavailable {
		t.Fatal("canceled request accepted")
	}
}
