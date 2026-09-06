//go:build integration

// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"waiting-room/internal/adminauth"
)

func enrollmentFixture(t *testing.T) (fixture, *LoginService, *EnrollmentService, string, EnrollmentSetup) {
	t.Helper()
	f, l, install := bootstrapFixture(t)
	r, err := l.Bootstrap(context.Background(), install.Token(), "admin1", testPassword, testPeer)
	if err != nil {
		t.Fatal(err)
	}
	e, err := NewEnrollmentService(f.store, l.passwords, "k1")
	if err != nil {
		t.Fatal(err)
	}
	setup, err := e.Begin(context.Background(), r.EnrollmentToken())
	if err != nil {
		t.Fatal(err)
	}
	return f, l, e, r.EnrollmentToken(), setup
}
func enrollmentCode(t *testing.T, f fixture, s EnrollmentSetup) string {
	t.Helper()
	var now time.Time
	if err := f.pool.QueryRow(context.Background(), "SELECT clock_timestamp()").Scan(&now); err != nil {
		t.Fatal(err)
	}
	return enrollmentCodeAt(t, s, uint64(now.Unix()/30))
}
func enrollmentCodeAt(t *testing.T, s EnrollmentSetup, counter uint64) string {
	t.Helper()
	key, err := s.ManualKey()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(key)
	if err != nil {
		t.Fatal(err)
	}
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], counter)
	mac := hmac.New(sha1.New, decoded)
	mac.Write(b[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 15
	return fmt.Sprintf("%06d", (binary.BigEndian.Uint32(sum[offset:offset+4])&0x7fffffff)%1000000)
}
func enrollmentCounts(t *testing.T, f fixture, wantCredential, wantRecovery, wantSession int) {
	t.Helper()
	var c, r, s int
	err := f.pool.QueryRow(context.Background(), "SELECT (SELECT count(*) FROM auth_totp_credentials),(SELECT count(*) FROM auth_recovery_codes),(SELECT count(*) FROM auth_sessions)").Scan(&c, &r, &s)
	if err != nil || c != wantCredential || r != wantRecovery || s != wantSession {
		t.Fatalf("enrollment counts %d/%d/%d expected %d/%d/%d, error %v", c, r, s, wantCredential, wantRecovery, wantSession, err)
	}
}

func TestEnrollmentServiceEndToEnd(t *testing.T) {
	f, l, e, token, setup := enrollmentFixture(t)
	ctx := context.Background()
	again, err := e.Begin(ctx, token)
	if err != nil || again.secret != setup.secret || !again.ExpiresAt().Equal(setup.ExpiresAt()) {
		t.Fatal("begin rotated or extended pending secret", err)
	}
	enrollmentCounts(t, f, 0, 0, 0)
	// Four failures must still permit a correct fifth reserved attempt.
	for range 4 {
		if _, err = e.Complete(ctx, token, "bad"); err != adminauth.ErrInvalidOrReplayed {
			t.Fatal("bad code did not fail", err)
		}
	}
	grant, err := e.Complete(ctx, token, enrollmentCode(t, f, setup))
	if err != nil || len(grant.SessionGrant().SessionToken()) != 43 {
		t.Fatal("registration failed", err)
	}
	enrollmentCounts(t, f, 1, 10, 1)
	me, err := NewSessionService(f.replica, "https://admin.example.test", false)
	if err != nil {
		t.Fatal(err)
	}
	view, err := me.Me(ctx, grant.SessionGrant().SessionToken())
	if err != nil || !view.MFAVerified || !view.TOTPEnrolled {
		t.Fatal("registered MFA session invalid", err)
	}
	seen := map[string]bool{}
	var firstHash string
	for i, raw := range grant.RecoveryCodes() {
		if len(raw) != 43 || seen[raw] {
			t.Fatal("bad recovery entropy shape/duplicate")
		}
		seen[raw] = true
		var hash string
		if err = f.pool.QueryRow(ctx, "SELECT code_hash FROM auth_recovery_codes WHERE user_id='admin1' AND slot=$1", i+1).Scan(&hash); err != nil || hash == raw {
			t.Fatal("recovery storage missing/plaintext", err)
		}
		if i == 0 {
			firstHash = hash
		}
	}
	valid, err := l.passwords.Verify(ctx, "wr-recovery-v1:"+grant.RecoveryCodes()[0], firstHash)
	if err != nil || !valid {
		t.Fatal("recovery hash cannot verify", err)
	}
	var consumed, cleared bool
	hash, _ := tokenHash(token)
	if err = f.pool.QueryRow(ctx, "SELECT consumed,key_id IS NULL AND sealed_secret IS NULL FROM auth_enrollment_challenges WHERE token_hash=$1", hash[:]).Scan(&consumed, &cleared); err != nil || !consumed || !cleared {
		t.Fatal("pending enrollment not erased", err)
	}
	if _, err = e.Begin(ctx, token); err != adminauth.ErrInvalidOrReplayed {
		t.Fatal("completed secret redisplayed", err)
	}
	if _, err = e.Complete(ctx, token, enrollmentCode(t, f, setup)); err != adminauth.ErrInvalidOrReplayed {
		t.Fatal("enrollment replay", err)
	}
	login, err := l.Login(ctx, "admin1", testPassword, testPeer)
	if err != nil || len(login.ChallengeToken()) != 43 || login.EnrollmentToken() != "" {
		t.Fatal("post-enroll login wrong state", err)
	}
	// Generate the already-consumed counter's code, independent of wall-clock rollover.
	var counter int64
	if err = f.pool.QueryRow(ctx, "SELECT last_counter FROM auth_totp_credentials").Scan(&counter); err != nil {
		t.Fatal(err)
	}
	// Stored counter must include the enrollment OTP; normal login cannot replay it.
	var keyID string
	var sealed []byte
	if err = f.pool.QueryRow(ctx, "SELECT key_id,sealed_secret FROM auth_totp_credentials").Scan(&keyID, &sealed); err != nil {
		t.Fatal(err)
	}
	active, err := f.store.vault.open(adminauth.CredentialRef{UserID: "admin1", Version: 1}, keyID, sealed)
	if err != nil || active != setup.secret || counter < 0 {
		t.Fatal("active encrypted credential mismatch", err)
	}
	if _, err = f.replica.CompleteTOTP(ctx, login.ChallengeToken(), enrollmentCodeAt(t, setup, uint64(counter))); err != adminauth.ErrInvalidOrReplayed {
		t.Fatal("enrollment counter replay accepted by login", err)
	}
}

func TestEnrollmentAttemptLimitAndTokenRotation(t *testing.T) {
	f, l, e, token, _ := enrollmentFixture(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	var invalid, other atomic.Int32
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := e.Complete(ctx, token, "malformed")
			if err == adminauth.ErrInvalidOrReplayed {
				invalid.Add(1)
			} else {
				other.Add(1)
			}
		}()
	}
	wg.Wait()
	if invalid.Load() != 16 || other.Load() != 0 {
		t.Fatal("invalid request race returned wrong result")
	}
	var attempts int
	if err := f.pool.QueryRow(ctx, "SELECT attempts FROM auth_enrollment_challenges WHERE NOT consumed").Scan(&attempts); err != nil || attempts != 5 {
		t.Fatal("attempt cap not shared", err)
	}
	if _, err := e.Begin(ctx, token); err != adminauth.ErrInvalidOrReplayed {
		t.Fatal("exhausted token restarted", err)
	}
	fresh, err := l.Login(ctx, "admin1", testPassword, testPeer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Begin(ctx, fresh.EnrollmentToken()); err != nil {
		t.Fatal("password retry cannot enroll", err)
	}
	if _, err = e.Begin(ctx, token); err != adminauth.ErrInvalidOrReplayed {
		t.Fatal("rotated token usable", err)
	}
	enrollmentCounts(t, f, 0, 0, 0)
}

func TestEnrollmentConcurrentFinalization(t *testing.T) {
	f, l, e, token, setup := enrollmentFixture(t)
	ctx := context.Background()
	replica, err := NewEnrollmentService(f.replica, l.passwords, "k1")
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := tokenHash(token)
	attempt, err := e.reserve(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := e.prepareRecovery(ctx)
	if err != nil {
		t.Fatal(err)
	}
	code := enrollmentCode(t, f, setup)
	var wg sync.WaitGroup
	var success, denied, other atomic.Int32
	// Prepared KDF output, racing the final DB phase in two independent pools.
	for i := range 16 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			svc := e
			if i%2 == 1 {
				svc = replica
			}
			_, err := svc.finish(ctx, attempt, code, bundle)
			switch err {
			case nil:
				success.Add(1)
			case adminauth.ErrInvalidOrReplayed:
				denied.Add(1)
			default:
				other.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if success.Load() != 1 || denied.Load() != 15 || other.Load() != 0 {
		t.Fatalf("single winner violated %d/%d/%d", success.Load(), denied.Load(), other.Load())
	}
	enrollmentCounts(t, f, 1, 10, 1)
}

func TestEnrollmentStaleStateAndRollback(t *testing.T) {
	f, _, e, token, setup := enrollmentFixture(t)
	ctx := context.Background()
	hash, _ := tokenHash(token)
	attempt, err := e.reserve(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := e.prepareRecovery(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Each mutation is restored within this one fresh schema; no user data involved.
	for _, tc := range []struct {
		name, change, restore string
		want                  error
	}{
		{"disabled", "UPDATE auth_accounts SET enabled=false", "UPDATE auth_accounts SET enabled=true", adminauth.ErrInvalidOrReplayed},
		{"policy", "UPDATE auth_policy SET version=2", "UPDATE auth_policy SET version=1", adminauth.ErrInvalidOrReplayed},
		{"off", "UPDATE auth_policy SET totp_enabled=false", "UPDATE auth_policy SET totp_enabled=true", adminauth.ErrInvalidOrReplayed},
		{"user-version", "UPDATE auth_accounts SET session_version=2", "UPDATE auth_accounts SET session_version=1", adminauth.ErrInvalidOrReplayed},
		{"recovery-insert", "ALTER TABLE auth_recovery_codes ADD CONSTRAINT fixture_reject CHECK (false)", "ALTER TABLE auth_recovery_codes DROP CONSTRAINT fixture_reject", adminauth.ErrAuthUnavailable},
		{"session-insert", "ALTER TABLE auth_sessions ADD CONSTRAINT fixture_reject CHECK (false)", "ALTER TABLE auth_sessions DROP CONSTRAINT fixture_reject", adminauth.ErrAuthUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			execSQL(t, f.pool, tc.change)
			_, err := e.finish(ctx, attempt, enrollmentCode(t, f, setup), bundle)
			if err != tc.want {
				t.Fatal("stale/failure accepted", err)
			}
			enrollmentCounts(t, f, 0, 0, 0)
			execSQL(t, f.pool, tc.restore)
		})
	}
	// Choose a well-formed code outside a broad window, avoiding adjacent collisions.
	var now time.Time
	if err = f.pool.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		t.Fatal(err)
	}
	used := map[string]bool{}
	for delta := int64(-2); delta <= 2; delta++ {
		used[enrollmentCodeAt(t, setup, uint64(now.Unix()/30+delta))] = true
	}
	wrong := ""
	for i := 0; i < 1000000; i++ {
		candidate := fmt.Sprintf("%06d", i)
		if !used[candidate] {
			wrong = candidate
			break
		}
	}
	if _, err = e.finish(ctx, attempt, wrong, bundle); err != adminauth.ErrInvalidOrReplayed {
		t.Fatal("invalid OTP accepted", err)
	}
	if _, err = e.finish(ctx, attempt, enrollmentCode(t, f, setup), bundle); err != nil {
		t.Fatal("rollback consumed proof", err)
	}
	enrollmentCounts(t, f, 1, 10, 1)
}

func TestEnrollmentExpiryAfterLockWait(t *testing.T) {
	f, _, e, token, setup := enrollmentFixture(t)
	ctx := context.Background()
	hash, _ := tokenHash(token)
	attempt, err := e.reserve(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := e.prepareRecovery(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Keep the schema's five-minute upper bound while shortening remaining time.
	execSQL(t, f.pool, "UPDATE auth_enrollment_challenges SET created_at=clock_timestamp()-interval '4 minutes',expires_at=clock_timestamp()+interval '1 second'")
	tx, err := f.other.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(tx)
	if _, err = tx.Exec(ctx, "SELECT token_hash FROM auth_enrollment_challenges FOR UPDATE"); err != nil {
		t.Fatal(err)
	}
	code := enrollmentCode(t, f, setup)
	result := make(chan error, 1)
	go func() { _, err := e.finish(ctx, attempt, code, bundle); result <- err }()
	blocked := false
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		err = f.other.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=current_setting('application_name') AND wait_event_type='Lock' AND query LIKE 'SELECT user_id,user_version,%')").Scan(&blocked)
		if err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("no enrollment lock wait")
	}
	for {
		var expired bool
		if err = f.other.QueryRow(ctx, "SELECT expires_at<=clock_timestamp() FROM auth_enrollment_challenges").Scan(&expired); err != nil {
			t.Fatal(err)
		}
		if expired {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-result; err != adminauth.ErrInvalidOrReplayed {
		t.Fatal("expired during lock wait accepted", err)
	}
	enrollmentCounts(t, f, 0, 0, 0)
}
