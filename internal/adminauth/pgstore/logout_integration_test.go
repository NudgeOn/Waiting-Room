//go:build integration

// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"waiting-room/internal/adminauth"
)

func TestLogoutDurableReplayConcurrentAndBound(t *testing.T) {
	f, s, g := sessionFixture(t)
	ctx := context.Background()
	execSQL(t, f.pool, Migration005)
	execSQL(t, f.pool, Migration010)
	execSQL(t, f.pool, Migration013)
	s.store.authAudit = true
	key := "logout-shared-command-key"
	var first, replays, bad atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := sessionRequest(g)
			r.Header.Set("Idempotency-Key", key)
			replay, err := s.LogoutReplay(ctx, g.SessionToken(), r)
			if err != nil {
				bad.Add(1)
			} else if replay {
				replays.Add(1)
			} else {
				first.Add(1)
			}
		}()
	}
	wg.Wait()
	if first.Load() != 1 || replays.Load() != 7 || bad.Load() != 0 {
		t.Fatal("logout race", first.Load(), replays.Load(), bad.Load())
	}
	var count int
	if f.pool.QueryRow(ctx, "SELECT count(*) FROM control_audit WHERE action='auth.logout'").Scan(&count) != nil || count != 1 {
		t.Fatal("duplicate logout audit", count)
	}
	if _, err := s.Me(ctx, g.SessionToken()); !errors.Is(err, adminauth.ErrUnauthenticated) {
		t.Fatal("logout proof authenticated a session", err)
	}
	replica, _ := NewSessionService(f.replica, "https://admin.test", false)
	r := sessionRequest(g)
	r.Header.Set("Idempotency-Key", key)
	if replay, err := replica.LogoutReplay(ctx, g.SessionToken(), r); err != nil || !replay {
		t.Fatal("restart/pool replay", err)
	}
	for _, change := range []struct {
		header, value string
		want          error
	}{
		{"Origin", "https://evil.test", adminauth.ErrForbidden}, {"X-CSRF-Token", "invalid", adminauth.ErrForbidden}, {"Idempotency-Key", "another-logout-command", ErrLogoutConflict},
	} {
		r := sessionRequest(g)
		r.Header.Set("Idempotency-Key", key)
		r.Header.Set(change.header, change.value)
		if _, err := s.LogoutReplay(ctx, g.SessionToken(), r); !errors.Is(err, change.want) {
			t.Fatal("logout binding", change.header, err)
		}
	}
	execSQL(t, f.pool, "UPDATE auth_logout_replays SET created_at=clock_timestamp()-interval '25 hours',expires_at=clock_timestamp()-interval '1 hour'")
	if _, err := s.LogoutReplay(ctx, g.SessionToken(), r); !errors.Is(err, adminauth.ErrUnauthenticated) {
		t.Fatal("expired replay accepted", err)
	}
}
func TestLogoutReplayAuditFailureRollsBackDeletionAndReceipt(t *testing.T) {
	f, s, g := sessionFixture(t)
	ctx := context.Background()
	execSQL(t, f.pool, Migration005)
	execSQL(t, f.pool, Migration010)
	execSQL(t, f.pool, Migration013)
	s.store.authAudit = true
	execSQL(t, f.pool, "ALTER TABLE control_audit ADD CONSTRAINT reject_logout CHECK(action<>'auth.logout')")
	r := sessionRequest(g)
	r.Header.Set("Idempotency-Key", "logout-audit-rollback-key")
	if _, err := s.LogoutReplay(ctx, g.SessionToken(), r); !errors.Is(err, adminauth.ErrAuthUnavailable) {
		t.Fatal("logout committed without audit", err)
	}
	if _, err := s.Me(ctx, g.SessionToken()); err != nil {
		t.Fatal("session deleted before audit", err)
	}
	var count int
	f.pool.QueryRow(ctx, "SELECT count(*) FROM auth_logout_replays").Scan(&count)
	if count != 0 {
		t.Fatal("uncommitted receipt")
	}
	execSQL(t, f.pool, "ALTER TABLE control_audit DROP CONSTRAINT reject_logout")
	if replay, err := s.LogoutReplay(ctx, g.SessionToken(), r); err != nil || replay {
		t.Fatal("retry after rollback", err)
	}
}
