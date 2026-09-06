//go:build integration

// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"waiting-room/internal/adminauth"
)

const testPassword = "local password fixture 2026"

func TestPasswordLoginPolicyChangeDuringVerification(t *testing.T) {
	f, l := loginFixture(t)
	ctx := context.Background()
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(tx)
	// Uncommitted update is invisible to the snapshot read; final FOR SHARE must wait.
	if _, err = tx.Exec(ctx, "UPDATE auth_policy SET version=2"); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { _, err := l.Login(ctx, "admin1", testPassword, testPeer); result <- err }()
	blocked := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		err = f.other.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=current_setting('application_name') AND wait_event_type='Lock' AND query LIKE 'SELECT mode,totp_enabled,%')").Scan(&blocked)
		if err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("login did not reach final policy lock")
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-result; err != adminauth.ErrUnauthenticated {
		t.Fatal("stale password proof accepted after policy update", err)
	}
	var count int
	if err = f.pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM auth_sessions)+(SELECT count(*) FROM auth_totp_challenges)").Scan(&count); err != nil || count != 0 {
		t.Fatal("stale proof minted credential")
	}
}

var testPeer = netip.MustParseAddr("127.0.0.1")

func loginFixture(t *testing.T) (fixture, *LoginService) {
	t.Helper()
	f := setup(t)
	seedUser(t, f)
	h, err := adminauth.NewPasswordHasher(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := h.Hash(context.Background(), testPassword)
	if err != nil {
		t.Fatal(err)
	}
	execSQL(t, f.pool, "INSERT INTO auth_password_credentials (user_id,version,password_hash) VALUES ('admin1',1,$1)", encoded.StorageValue())
	l, err := NewLoginService(f.store, h, [32]byte{1})
	if err != nil {
		t.Fatal(err)
	}
	return f, l
}
func TestPasswordLoginTOTPFlow(t *testing.T) {
	f, l := loginFixture(t)
	ctx := context.Background()
	result, err := l.Login(ctx, "admin1", testPassword, testPeer)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ChallengeToken()) != 43 || result.SessionGrant().SessionToken() != "" {
		t.Fatal("TOTP bypass")
	}
	var sessions int
	if err = f.pool.QueryRow(ctx, "SELECT count(*) FROM auth_sessions").Scan(&sessions); err != nil || sessions != 0 {
		t.Fatal("premature session")
	}
	grant, err := f.replica.CompleteTOTP(ctx, result.ChallengeToken(), currentCode(t, f.pool))
	if err != nil || len(grant.SessionToken()) != 43 {
		t.Fatal("password to TOTP to session failed", err)
	}
	assertCounts(t, f, 1, 1, true)
	// New password login invalidates previous pending challenges, not prior sessions.
	first, err := l.Login(ctx, "admin1", testPassword, testPeer)
	if err != nil {
		t.Fatal(err)
	}
	last, err := l.Login(ctx, "admin1", testPassword, testPeer)
	if err != nil {
		t.Fatal(err)
	}
	if first.ChallengeToken() == last.ChallengeToken() {
		t.Fatal("challenge not rotated")
	}
	if _, err = f.store.CompleteTOTP(ctx, first.ChallengeToken(), currentCode(t, f.pool)); err != adminauth.ErrInvalidOrReplayed {
		t.Fatal("replaced challenge usable")
	}
	var active int
	if err = f.pool.QueryRow(ctx, "SELECT count(*) FROM auth_totp_challenges WHERE NOT consumed").Scan(&active); err != nil || active != 1 {
		t.Fatal("unbounded pending challenges")
	}
}
func TestPasswordLoginOFFAndEnrollmentRestricted(t *testing.T) {
	for _, off := range []bool{true, false} {
		t.Run(fmt.Sprint("off=", off), func(t *testing.T) {
			f, l := loginFixture(t)
			ctx := context.Background()
			execSQL(t, f.pool, "DELETE FROM auth_totp_credentials")
			if off {
				execSQL(t, f.pool, "UPDATE auth_policy SET totp_enabled=false")
			}
			result, err := l.Login(ctx, "admin1", testPassword, testPeer)
			if !off {
				if err != nil || len(result.EnrollmentToken()) != 43 || result.ChallengeToken() != "" || result.SessionGrant().SessionToken() != "" {
					t.Fatal("unenrolled bypass")
				}
				return
			}
			if err != nil || result.ChallengeToken() != "" || len(result.SessionGrant().SessionToken()) != 43 {
				t.Fatal("explicit OFF failed", err)
			}
			var mfa bool
			if err = f.pool.QueryRow(ctx, "SELECT mfa_verified FROM auth_sessions").Scan(&mfa); err != nil || mfa {
				t.Fatal("password session falsely marked MFA")
			}
		})
	}
}
func TestPasswordLoginInvalidAndAccountLimit(t *testing.T) {
	f, l := loginFixture(t)
	ctx := context.Background()
	for range LoginAccountLimit {
		result, err := l.Login(ctx, "admin1", "wrong-password", testPeer)
		if err != adminauth.ErrUnauthenticated || result.ChallengeToken() != "" {
			t.Fatal("bad password")
		}
	}
	if _, err := l.Login(ctx, "admin1", testPassword, testPeer); err != adminauth.ErrUnauthenticated {
		t.Fatal("account throttle bypass")
	}
	if _, err := l.Login(ctx, "missing", testPassword, testPeer); err != adminauth.ErrUnauthenticated {
		t.Fatal("user enumeration")
	}
	execSQL(t, f.pool, "UPDATE auth_login_buckets SET started_at=clock_timestamp()-interval '2 minutes',expires_at=clock_timestamp()-interval '1 minute'")
	execSQL(t, f.pool, "UPDATE auth_accounts SET enabled=false")
	if _, err := l.Login(ctx, "admin1", testPassword, testPeer); err != adminauth.ErrUnauthenticated {
		t.Fatal("disabled account")
	}
	var count int
	if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM auth_totp_challenges").Scan(&count); err != nil || count != 0 {
		t.Fatal("unauthorized challenge")
	}
}
func TestPasswordThrottleSharedPoolsAndCardinality(t *testing.T) {
	f := setup(t)
	a := &LoginService{store: f.store, fingerprintKey: [32]byte{1}}
	b := &LoginService{store: f.replica, fingerprintKey: [32]byte{1}}
	var pass, bad atomic.Int32
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			l := a
			if i%2 == 1 {
				l = b
			}
			err := l.reserveAttempt(context.Background(), "same", fmt.Sprint("source", i))
			if err == nil {
				pass.Add(1)
			} else if err != adminauth.ErrUnauthenticated {
				bad.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if pass.Load() != LoginAccountLimit || bad.Load() != 0 {
		t.Fatalf("shared account limit pass=%d unexpected=%d", pass.Load(), bad.Load())
	}
	// Reset only this test's isolated bucket table; test source and global dimensions.
	execSQL(t, f.pool, "DELETE FROM auth_login_buckets")
	successes := 0
	for i := range 25 {
		if err := a.reserveAttempt(context.Background(), fmt.Sprint("user", i), "same-source"); err == nil {
			successes++
		} else if err != adminauth.ErrUnauthenticated {
			t.Fatal(err)
		}
	}
	if successes != LoginSourceLimit {
		t.Fatal("source throttle")
	}
	execSQL(t, f.pool, "DELETE FROM auth_login_buckets")
	successes = 0
	for i := range 80 {
		if err := a.reserveAttempt(context.Background(), fmt.Sprint("user", i), fmt.Sprint("source", i)); err == nil {
			successes++
		} else if err != adminauth.ErrUnauthenticated {
			t.Fatal(err)
		}
	}
	if successes != LoginInstallationLimit {
		t.Fatal("installation throttle")
	}
	var rows int
	if err := f.pool.QueryRow(context.Background(), "SELECT count(*) FROM auth_login_buckets").Scan(&rows); err != nil || rows > 1+2*LoginInstallationLimit {
		t.Fatal("unbounded bucket rows")
	}
	// Expired identifiers are cleaned on the next admitted attempt.
	execSQL(t, f.pool, "UPDATE auth_login_buckets SET started_at=clock_timestamp()-interval '2 minutes',expires_at=clock_timestamp()-interval '1 minute'")
	if err := b.reserveAttempt(context.Background(), "fresh", "fresh"); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(context.Background(), "SELECT count(*) FROM auth_login_buckets").Scan(&rows); err != nil || rows != 3 {
		t.Fatal("expired bucket cleanup")
	}
}
func TestPasswordSnapshotChangesFailClosed(t *testing.T) {
	for _, sql := range []string{
		"UPDATE auth_password_credentials SET version=2",
		"UPDATE auth_password_credentials SET password_hash='changed'",
		"UPDATE auth_accounts SET session_version=2",
		"UPDATE auth_accounts SET enabled=false",
		"UPDATE auth_accounts SET role='viewer'",
		"UPDATE auth_policy SET version=2",
		"UPDATE auth_policy SET totp_enabled=false",
	} {
		t.Run(strings.Fields(sql)[1]+"/"+strings.Join(strings.Fields(sql)[3:], ""), func(t *testing.T) {
			f, l := loginFixture(t)
			before, err := l.snapshot(context.Background(), "admin1")
			if err != nil {
				t.Fatal(err)
			}
			execSQL(t, f.pool, sql)
			result, err := l.finishPassword(context.Background(), before)
			if err != adminauth.ErrUnauthenticated || result.ChallengeToken() != "" || result.SessionGrant().SessionToken() != "" {
				t.Fatal("stale password proof accepted", err)
			}
		})
	}
}
func TestPasswordResultInsertFailureRollsBack(t *testing.T) {
	f, l := loginFixture(t)
	ctx := context.Background()
	first, err := l.Login(ctx, "admin1", testPassword, testPeer)
	if err != nil {
		t.Fatal(err)
	}
	execSQL(t, f.pool, "CREATE FUNCTION test_reject_login() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test insert failure'; END $$")
	execSQL(t, f.pool, "CREATE TRIGGER test_reject_login BEFORE INSERT ON auth_totp_challenges FOR EACH ROW EXECUTE FUNCTION test_reject_login()")
	result, err := l.Login(ctx, "admin1", testPassword, testPeer)
	if err != adminauth.ErrAuthUnavailable || result.ChallengeToken() != "" {
		t.Fatal("failed insert returned credential")
	}
	hash, _ := tokenHash(first.ChallengeToken())
	var consumed bool
	if err = f.pool.QueryRow(ctx, "SELECT consumed FROM auth_totp_challenges WHERE token_hash=$1", hash[:]).Scan(&consumed); err != nil || consumed {
		t.Fatal("challenge replacement not rolled back")
	}
	var used int
	if err = f.pool.QueryRow(ctx, "SELECT used FROM auth_login_buckets WHERE bucket_key='installation'").Scan(&used); err != nil || used != 2 {
		t.Fatal("failed login refunded attempt")
	}
}
