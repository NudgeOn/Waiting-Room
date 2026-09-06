//go:build integration

// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"context"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
)

func TestAuthHTTPSharedDatabaseLimit(t *testing.T) {
	for _, sourceOnly := range []bool{true, false} {
		t.Run(map[bool]string{true: "source", false: "installation"}[sourceOnly], func(t *testing.T) {
			f := setup(t)
			a := &LoginService{store: f.store, fingerprintKey: [32]byte{1}}
			b := &LoginService{store: f.replica, fingerprintKey: [32]byte{1}}
			var wg sync.WaitGroup
			var ok, denied, bad atomic.Int32
			for i := range 80 {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					svc := a
					if i%2 == 1 {
						svc = b
					}
					peer := netip.AddrFrom4([4]byte{192, 0, 2, 1})
					if !sourceOnly {
						peer = netip.AddrFrom4([4]byte{192, 0, 2, byte(i + 1)})
					}
					switch svc.ReserveAuthHTTP(context.Background(), peer) {
					case nil:
						ok.Add(1)
					case ErrAuthHTTPRateLimit:
						denied.Add(1)
					default:
						bad.Add(1)
					}
				}(i)
			}
			wg.Wait()
			want := int32(AuthHTTPSourceLimit)
			if !sourceOnly {
				want = AuthHTTPInstallationLimit
			}
			if ok.Load() != want || denied.Load() != 80-want || bad.Load() != 0 {
				t.Fatalf("cap=%d/%d/%d", ok.Load(), denied.Load(), bad.Load())
			}
			execSQL(t, f.pool, "UPDATE auth_login_buckets SET started_at=clock_timestamp()-interval '2 minutes',expires_at=clock_timestamp()-interval '1 minute'")
			if err := a.ReserveAuthHTTP(context.Background(), netip.MustParseAddr("192.0.2.1")); err != nil {
				t.Fatal("expired HTTP budget not renewed", err)
			}
		})
	}
	t.Run("namespace-cleanup", func(t *testing.T) {
		f := setup(t)
		l := &LoginService{store: f.store, fingerprintKey: [32]byte{1}}
		ctx := context.Background()
		execSQL(t, f.pool, "INSERT INTO auth_login_buckets VALUES ('source:fixture',clock_timestamp()-interval '2 minutes',clock_timestamp()-interval '1 minute',1)")
		if err := l.ReserveAuthHTTP(ctx, testPeer); err != nil {
			t.Fatal(err)
		}
		var exists bool
		if err := f.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM auth_login_buckets WHERE bucket_key='source:fixture')").Scan(&exists); err != nil || !exists {
			t.Fatal("HTTP pruned password namespace")
		}
		execSQL(t, f.pool, "UPDATE auth_login_buckets SET started_at=clock_timestamp()-interval '2 minutes',expires_at=clock_timestamp()-interval '1 minute' WHERE bucket_key LIKE 'http-%'")
		if err := l.reserveAttempt(ctx, "admin", "127.0.0.1"); err != nil {
			t.Fatal(err)
		}
		if err := f.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM auth_login_buckets WHERE bucket_key='http-installation')").Scan(&exists); err != nil || !exists {
			t.Fatal("password pruned HTTP namespace")
		}
	})
}
