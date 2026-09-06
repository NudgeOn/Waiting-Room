//go:build integration

// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"waiting-room/internal/adminauth"
)

func sessionFixture(t *testing.T) (fixture, *SessionService, Grant) {
	t.Helper()
	f := setup(t)
	seedUser(t, f)
	raw, sh, err := adminauth.NewCSRFToken()
	if err != nil {
		t.Fatal(err)
	}
	csrf, ch, err := adminauth.NewCSRFToken()
	if err != nil {
		t.Fatal(err)
	}
	execSQL(t, f.pool, "INSERT INTO auth_sessions (token_hash,csrf_hash,user_id,policy_version,user_version,mfa_verified,created_at,last_seen_at) SELECT $1,$2,'admin1',1,1,true,t,t FROM (SELECT clock_timestamp()-interval '1 minute' AS t) stamp", sh[:], ch[:])
	service, err := NewSessionService(f.store, "https://admin.test", false)
	if err != nil {
		t.Fatal(err)
	}
	return f, service, Grant{raw, csrf}
}
func sessionRequest(g Grant) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "https://admin.test/api/admin/v1/auth/logout", nil)
	r.Header.Set("Origin", "https://admin.test")
	r.Header.Set("X-CSRF-Token", g.CSRFToken())
	return r
}
func TestSessionReadTouchLogout(t *testing.T) {
	f, s, g := sessionFixture(t)
	ctx := context.Background()
	a, err := s.Me(ctx, g.SessionToken())
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Me(ctx, g.SessionToken())
	if err != nil || a.LastSeenAt != b.LastSeenAt {
		t.Fatal("GET extended session", err)
	}
	touched, err := s.Touch(ctx, g.SessionToken(), sessionRequest(g))
	if err != nil || !touched.LastSeenAt.After(a.LastSeenAt) || touched.CreatedAt != a.CreatedAt || touched.AbsoluteExpiresAt != a.AbsoluteExpiresAt {
		t.Fatal("invalid idle refresh", err)
	}
	if err = s.Logout(ctx, g.SessionToken(), sessionRequest(g)); err != nil {
		t.Fatal(err)
	}
	replica, _ := NewSessionService(f.replica, "https://admin.test", false)
	for _, reader := range []*SessionService{s, replica} {
		if _, err = reader.Me(ctx, g.SessionToken()); err != adminauth.ErrUnauthenticated {
			t.Fatal("logout not shared")
		}
	}
	if _, err = s.Touch(ctx, g.SessionToken(), sessionRequest(g)); err != adminauth.ErrUnauthenticated {
		t.Fatal("revived logout")
	}
}
func TestSessionExpiryAndRevocation(t *testing.T) {
	cases := []string{
		"UPDATE auth_sessions SET created_at=clock_timestamp()-interval '2 hours',last_seen_at=clock_timestamp()-interval '31 minutes'",
		"UPDATE auth_sessions SET created_at=clock_timestamp()-interval '8 hours'",
		"UPDATE auth_sessions SET last_seen_at=clock_timestamp()+interval '1 minute'",
		"UPDATE auth_sessions SET mfa_verified=false",
		"UPDATE auth_accounts SET enabled=false",
		"UPDATE auth_accounts SET session_version=2",
		"UPDATE auth_policy SET version=2",
		"DELETE FROM auth_totp_credentials",
	}
	for i, sql := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			f, s, g := sessionFixture(t)
			execSQL(t, f.pool, sql)
			if _, err := s.Me(context.Background(), g.SessionToken()); err != adminauth.ErrUnauthenticated {
				t.Fatal("invalid session read", err)
			}
			if _, err := s.Touch(context.Background(), g.SessionToken(), sessionRequest(g)); err != adminauth.ErrUnauthenticated {
				t.Fatal("invalid session extended", err)
			}
		})
	}
}
func TestSessionCSRFFailuresDoNotMutate(t *testing.T) {
	_, s, g := sessionFixture(t)
	ctx := context.Background()
	before, err := s.Me(ctx, g.SessionToken())
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"missing", "cross-origin", "duplicate", "wrong-token", "get"} {
		r := sessionRequest(g)
		switch kind {
		case "missing":
			r.Header.Del("X-CSRF-Token")
		case "cross-origin":
			r.Header.Set("Origin", "https://evil.test")
		case "duplicate":
			r.Header.Add("Origin", "https://admin.test")
		case "wrong-token":
			token, _, _ := adminauth.NewCSRFToken()
			r.Header.Set("X-CSRF-Token", token)
		case "get":
			r.Method = "GET"
		}
		if _, err = s.Touch(ctx, g.SessionToken(), r); err != adminauth.ErrForbidden {
			t.Fatal("CSRF refresh accepted", kind, err)
		}
		if err = s.Logout(ctx, g.SessionToken(), r); err != adminauth.ErrForbidden {
			t.Fatal("CSRF logout accepted", kind, err)
		}
	}
	after, err := s.Me(ctx, g.SessionToken())
	if err != nil || after.LastSeenAt != before.LastSeenAt {
		t.Fatal("CSRF changed state")
	}
}
func TestSessionConcurrentLogoutAndTouch(t *testing.T) {
	f, s, g := sessionFixture(t)
	other, _ := NewSessionService(f.replica, "https://admin.test", false)
	var deletes, bad atomic.Int32
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var err error
			if i%2 == 0 {
				err = s.Logout(context.Background(), g.SessionToken(), sessionRequest(g))
				if err == nil {
					deletes.Add(1)
				}
			} else {
				_, err = other.Touch(context.Background(), g.SessionToken(), sessionRequest(g))
			}
			if err != nil && err != adminauth.ErrUnauthenticated {
				bad.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if deletes.Load() != 1 || bad.Load() != 0 {
		t.Fatalf("deletes=%d unexpected=%d", deletes.Load(), bad.Load())
	}
	if _, err := other.Me(context.Background(), g.SessionToken()); err != adminauth.ErrUnauthenticated {
		t.Fatal("session resurrected")
	}
}
func TestSessionPolicyUpdateLockWait(t *testing.T) {
	f, s, g := sessionFixture(t)
	ctx := context.Background()
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(tx)
	if _, err = tx.Exec(ctx, "UPDATE auth_policy SET version=2"); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { _, err := s.Touch(ctx, g.SessionToken(), sessionRequest(g)); result <- err }()
	blocked := false
	deadline := time.Now().Add(3 * time.Second)
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
		t.Fatal("did not wait on policy lock")
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-result; err != adminauth.ErrUnauthenticated {
		t.Fatal("stale session renewed after policy commit", err)
	}
}
func TestSessionLatestRoleAndExplicitOFF(t *testing.T) {
	f, s, g := sessionFixture(t)
	execSQL(t, f.pool, "UPDATE auth_accounts SET role='viewer'")
	execSQL(t, f.pool, "UPDATE auth_policy SET totp_enabled=false")
	execSQL(t, f.pool, "UPDATE auth_sessions SET mfa_verified=false")
	execSQL(t, f.pool, "DELETE FROM auth_totp_credentials")
	view, err := s.Me(context.Background(), g.SessionToken())
	if err != nil || view.Role != adminauth.Viewer || view.TOTPEnrolled || view.MFAVerified {
		t.Fatal("OFF or current role ignored", err)
	}
	for _, c := range view.Capabilities {
		if c.Action == adminauth.WriteConfig {
			t.Fatal("stale Admin permission")
		}
	}
}

func TestSessionMutationFailureRollsBack(t *testing.T) {
	f, s, g := sessionFixture(t)
	ctx := context.Background()
	before, err := s.Me(ctx, g.SessionToken())
	if err != nil {
		t.Fatal(err)
	}
	execSQL(t, f.pool, "CREATE FUNCTION test_reject_session_write() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test session write failure'; END $$")
	execSQL(t, f.pool, "CREATE TRIGGER test_reject_session_write BEFORE UPDATE OR DELETE ON auth_sessions FOR EACH ROW EXECUTE FUNCTION test_reject_session_write()")
	if _, err = s.Touch(ctx, g.SessionToken(), sessionRequest(g)); err != adminauth.ErrAuthUnavailable {
		t.Fatal("failed touch acknowledged", err)
	}
	if err = s.Logout(ctx, g.SessionToken(), sessionRequest(g)); err != adminauth.ErrAuthUnavailable {
		t.Fatal("failed logout acknowledged", err)
	}
	after, err := s.Me(ctx, g.SessionToken())
	if err != nil || after.LastSeenAt != before.LastSeenAt {
		t.Fatal("failed write changed session")
	}
}
func TestSessionExpiresDuringLockWait(t *testing.T) {
	f, s, g := sessionFixture(t)
	ctx := context.Background()
	execSQL(t, f.pool, "UPDATE auth_sessions SET created_at=clock_timestamp()-interval '1 hour',last_seen_at=clock_timestamp()-interval '30 minutes'+interval '300 milliseconds'")
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(tx)
	if _, err = tx.Exec(ctx, "SELECT token_hash FROM auth_sessions FOR UPDATE"); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { _, err := s.Me(ctx, g.SessionToken()); result <- err }()
	ready := false
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		err = f.other.QueryRow(ctx, "SELECT clock_timestamp()>=last_seen_at+interval '30 minutes' AND EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=current_setting('application_name') AND wait_event_type='Lock') FROM auth_sessions").Scan(&ready)
		if err != nil {
			t.Fatal(err)
		}
		if ready {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ready {
		t.Fatal("expiry during wait not observed")
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-result; err != adminauth.ErrUnauthenticated {
		t.Fatal("session accepted using stale clock", err)
	}
}
