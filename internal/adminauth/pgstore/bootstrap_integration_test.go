//go:build integration

// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"bytes"
	"context"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"waiting-room/internal/adminauth"
)

func bootstrapFixture(t *testing.T) (fixture, *LoginService, BootstrapToken) {
	t.Helper()
	f := setup(t)
	h, err := adminauth.NewPasswordHasher(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	l, err := NewLoginService(f.store, h, [32]byte{1})
	if err != nil {
		t.Fatal(err)
	}
	token, err := f.store.IssueBootstrapToken(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return f, l, token
}

func TestBootstrapONRestrictedAndRetry(t *testing.T) {
	f, l, token := bootstrapFixture(t)
	ctx := context.Background()
	result, err := l.Bootstrap(ctx, token.Token(), "first-admin", testPassword, netip.MustParseAddr("::1"))
	if err != nil || len(result.EnrollmentToken()) != 43 || result.ChallengeToken() != "" || result.SessionGrant().SessionToken() != "" {
		t.Fatal("ON bootstrap did not restrict grant", err)
	}
	var complete, cleared bool
	var accounts, sessions int
	err = f.pool.QueryRow(ctx, "SELECT completed,token_hash IS NULL AND token_expires_at IS NULL,(SELECT count(*) FROM auth_accounts WHERE role='admin'),(SELECT count(*) FROM auth_sessions) FROM auth_bootstrap").Scan(&complete, &cleared, &accounts, &sessions)
	if err != nil || !complete || !cleared || accounts != 1 || sessions != 0 {
		t.Fatal("bootstrap atomic state incorrect", err)
	}
	me, err := NewSessionService(f.replica, "https://admin.example.test", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{token.Token(), result.EnrollmentToken()} {
		if _, err := me.Me(ctx, raw); err != adminauth.ErrUnauthenticated {
			t.Fatal("non-session token accepted", err)
		}
		if _, err := f.replica.CompleteTOTP(ctx, raw, "123456"); err != adminauth.ErrInvalidOrReplayed {
			t.Fatal("wrong token domain accepted", err)
		}
	}
	// A lost bootstrap response is recoverable through password authentication.
	retry, err := l.Login(ctx, "first-admin", testPassword, testPeer)
	if err != nil || len(retry.EnrollmentToken()) != 43 || retry.EnrollmentToken() == result.EnrollmentToken() || retry.SessionGrant().SessionToken() != "" {
		t.Fatal("enrollment retry failed", err)
	}
	oldHash, _ := tokenHash(result.EnrollmentToken())
	newHash, _ := tokenHash(retry.EnrollmentToken())
	var oldConsumed, currentBound bool
	err = f.pool.QueryRow(ctx, "SELECT (SELECT consumed FROM auth_enrollment_challenges WHERE token_hash=$1),EXISTS(SELECT 1 FROM auth_enrollment_challenges WHERE token_hash=$2 AND user_id='first-admin' AND policy_version=1 AND user_version=1 AND NOT consumed AND expires_at=created_at+interval '5 minutes')", oldHash[:], newHash[:]).Scan(&oldConsumed, &currentBound)
	if err != nil || !oldConsumed || !currentBound {
		t.Fatal("restricted token rotation/binding failed", err)
	}
	if _, err = l.Bootstrap(ctx, token.Token(), "second-admin", testPassword, testPeer); err != adminauth.ErrUnauthenticated {
		t.Fatal("install token reused", err)
	}
	if _, err = f.replica.IssueBootstrapToken(ctx); err != adminauth.ErrUnauthenticated {
		t.Fatal("bootstrap reopened", err)
	}
	// Test-only deletion proves the tombstone is not inferred from account count.
	execSQL(t, f.pool, "DELETE FROM auth_enrollment_challenges; DELETE FROM auth_password_credentials; DELETE FROM auth_accounts")
	if _, err = f.store.IssueBootstrapToken(ctx); err != adminauth.ErrUnauthenticated {
		t.Fatal("account deletion reopened bootstrap", err)
	}
}

func TestBootstrapOFFSession(t *testing.T) {
	f, l, token := bootstrapFixture(t)
	ctx := context.Background()
	execSQL(t, f.pool, "UPDATE auth_policy SET totp_enabled=false")
	result, err := l.Bootstrap(ctx, token.Token(), "first-admin", testPassword, netip.MustParseAddr("::ffff:127.0.0.1"))
	if err != nil || result.EnrollmentToken() != "" || result.ChallengeToken() != "" || len(result.SessionGrant().SessionToken()) != 43 {
		t.Fatal("explicit OFF bootstrap failed", err)
	}
	service, err := NewSessionService(f.replica, "https://admin.example.test", false)
	if err != nil {
		t.Fatal(err)
	}
	view, err := service.Me(ctx, result.SessionGrant().SessionToken())
	if err != nil || view.UserID != "first-admin" || view.Role != adminauth.Admin || view.MFAVerified || view.TOTPEnrolled {
		t.Fatal("OFF session projection incorrect", err)
	}
}

func TestBootstrapTokenRotationExpiryAndPeer(t *testing.T) {
	f, l, first := bootstrapFixture(t)
	ctx := context.Background()
	second, err := f.replica.IssueBootstrapToken(ctx)
	if err != nil || first.Token() == second.Token() {
		t.Fatal("install rotation failed", err)
	}
	hash, _ := tokenHash(second.Token())
	var stored []byte
	var ttl bool
	if err = f.pool.QueryRow(ctx, "SELECT token_hash,token_expires_at>clock_timestamp() AND token_expires_at<=clock_timestamp()+interval '15 minutes' FROM auth_bootstrap").Scan(&stored, &ttl); err != nil || !ttl || !bytes.Equal(stored, hash[:]) {
		t.Fatal("install hash/TTL incorrect", err)
	}
	for _, tc := range []struct {
		raw  string
		peer netip.Addr
	}{{first.Token(), testPeer}, {second.Token(), netip.MustParseAddr("203.0.113.1")}} {
		if _, err = l.Bootstrap(ctx, tc.raw, "first-admin", testPassword, tc.peer); err != adminauth.ErrUnauthenticated {
			t.Fatal("invalid bootstrap admitted", err)
		}
	}
	execSQL(t, f.pool, "UPDATE auth_bootstrap SET token_expires_at=clock_timestamp()-interval '1 second'")
	if _, err = l.Bootstrap(ctx, second.Token(), "first-admin", testPassword, testPeer); err != adminauth.ErrUnauthenticated {
		t.Fatal("expired bootstrap admitted", err)
	}
	var count int
	if err = f.pool.QueryRow(ctx, "SELECT count(*) FROM auth_accounts").Scan(&count); err != nil || count != 0 {
		t.Fatal("invalid request created account", err)
	}
}

func TestBootstrapConcurrentSingleWinner(t *testing.T) {
	f, a, token := bootstrapFixture(t)
	b, err := NewLoginService(f.replica, a.passwords, [32]byte{1})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var success, rejected, unexpected atomic.Int32
	start := make(chan struct{})
	for i, service := range []*LoginService{a, b} {
		wg.Add(1)
		go func(i int, l *LoginService) {
			defer wg.Done()
			<-start
			user := []string{"admin-a", "admin-b"}[i]
			_, err := l.Bootstrap(context.Background(), token.Token(), user, testPassword, testPeer)
			switch err {
			case nil:
				success.Add(1)
			case adminauth.ErrUnauthenticated:
				rejected.Add(1)
			default:
				unexpected.Add(1)
			}
		}(i, service)
	}
	close(start)
	wg.Wait()
	if success.Load() != 1 || rejected.Load() != 1 || unexpected.Load() != 0 {
		t.Fatalf("single winner violated: success=%d rejected=%d unexpected=%d", success.Load(), rejected.Load(), unexpected.Load())
	}
	var accounts, passwords, enrollment int
	err = f.pool.QueryRow(context.Background(), "SELECT (SELECT count(*) FROM auth_accounts),(SELECT count(*) FROM auth_password_credentials),(SELECT count(*) FROM auth_enrollment_challenges)").Scan(&accounts, &passwords, &enrollment)
	if err != nil || accounts != 1 || passwords != 1 || enrollment != 1 {
		t.Fatal("concurrent bootstrap state incorrect", err)
	}
}

func TestBootstrapStaleProofAndRollback(t *testing.T) {
	for _, change := range []string{"policy", "rotation", "expiry", "existing-account", "insert-failure"} {
		t.Run(change, func(t *testing.T) {
			f, l, token := bootstrapFixture(t)
			ctx := context.Background()
			hash, _ := tokenHash(token.Token())
			before, err := f.store.bootstrapSnapshot(ctx, hash)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := l.passwords.Hash(ctx, testPassword)
			if err != nil {
				t.Fatal(err)
			}
			expected := adminauth.ErrUnauthenticated
			switch change {
			case "policy":
				execSQL(t, f.pool, "UPDATE auth_policy SET version=version+1")
			case "rotation":
				if _, err = f.replica.IssueBootstrapToken(ctx); err != nil {
					t.Fatal(err)
				}
			case "expiry":
				execSQL(t, f.pool, "UPDATE auth_bootstrap SET token_expires_at=clock_timestamp()-interval '1 second'")
			case "existing-account":
				execSQL(t, f.pool, "INSERT INTO auth_accounts (id,role) VALUES ('existing','admin')")
			case "insert-failure":
				execSQL(t, f.pool, "ALTER TABLE auth_enrollment_challenges ADD CONSTRAINT fixture_reject CHECK (false)")
				expected = adminauth.ErrAuthUnavailable
			}
			if _, err = l.finishBootstrap(ctx, hash, "first-admin", encoded, before); err != expected {
				t.Fatal("stale/failed proof accepted", err)
			}
			var created, complete bool
			err = f.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM auth_accounts WHERE id='first-admin'),completed FROM auth_bootstrap").Scan(&created, &complete)
			if err != nil || created || complete {
				t.Fatal("partial bootstrap commit", err)
			}
			if change == "insert-failure" {
				execSQL(t, f.pool, "ALTER TABLE auth_enrollment_challenges DROP CONSTRAINT fixture_reject")
				if _, err = l.Bootstrap(ctx, token.Token(), "first-admin", testPassword, testPeer); err != nil {
					t.Fatal("rollback consumed install token", err)
				}
			}
		})
	}
}

func TestBootstrapExpiryAfterLockWait(t *testing.T) {
	f, l, token := bootstrapFixture(t)
	ctx := context.Background()
	hash, _ := tokenHash(token.Token())
	before, err := f.store.bootstrapSnapshot(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := l.passwords.Hash(ctx, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	execSQL(t, f.pool, "UPDATE auth_bootstrap SET token_expires_at=clock_timestamp()+interval '1 second'")
	tx, err := f.other.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(tx)
	if _, err = tx.Exec(ctx, "SELECT completed FROM auth_bootstrap FOR UPDATE"); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { _, e := l.finishBootstrap(ctx, hash, "first-admin", encoded, before); result <- e }()
	blocked := false
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		err = f.other.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=current_setting('application_name') AND wait_event_type='Lock' AND query LIKE 'SELECT completed FROM auth_bootstrap%')").Scan(&blocked)
		if err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("bootstrap did not wait on lock")
	}
	for {
		var expired bool
		if err = f.other.QueryRow(ctx, "SELECT token_expires_at<=clock_timestamp() FROM auth_bootstrap").Scan(&expired); err != nil {
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
	if err = <-result; err != adminauth.ErrUnauthenticated {
		t.Fatal("token expired during wait accepted", err)
	}
}

func TestBootstrapMigrationClosesExistingInstall(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	seedUser(t, f)
	// Only this fresh test schema; emulate applying migration 003 after accounts exist.
	execSQL(t, f.pool, "DROP TABLE auth_enrollment_challenges; DROP TABLE auth_bootstrap")
	execSQL(t, f.pool, Migration003)
	if _, err := f.store.IssueBootstrapToken(ctx); err != adminauth.ErrUnauthenticated {
		t.Fatal("existing installation bootstrap opened", err)
	}
	var complete bool
	if err := f.pool.QueryRow(ctx, "SELECT completed FROM auth_bootstrap").Scan(&complete); err != nil || !complete {
		t.Fatal("missing migration tombstone", err)
	}
}
