// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"waiting-room/internal/adminauth"
)

var ErrAuthHTTPRateLimit = errors.New("AUTH_RATE_LIMITED")

const AuthHTTPSourceLimit = 20
const AuthHTTPInstallationLimit = 60

// ReserveAuthHTTP counts all admitted auth HTTP attempts across login/OTP/recovery.
// Call before parsing bodies or doing KDF, with the real socket peer, never headers.
// Independent namespace/cleanup prevents lock inversion with password reservations.
func (l *LoginService) ReserveAuthHTTP(ctx context.Context, peer netip.Addr) error {
	source, ok := sourceValue(peer)
	if !ok {
		return adminauth.ErrForbidden
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := authTx(ctx, l.store)
	if err != nil {
		return err
	}
	defer rollback(tx)
	allowed := true
	for i, limit := range []int{AuthHTTPInstallationLimit, AuthHTTPSourceLimit} {
		key := "http-installation"
		if i == 1 {
			var now time.Time
			if err = tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
				return adminauth.ErrAuthUnavailable
			}
			key = "http-source:" + l.fingerprint(fmt.Sprintf("http-source/%d", now.Unix()/86400), source)
		}
		tag, err := tx.Exec(ctx, "INSERT INTO auth_login_buckets (bucket_key,started_at,expires_at,used) SELECT $1,t,t+interval '1 minute',1 FROM (SELECT clock_timestamp() AS t) stamp ON CONFLICT DO NOTHING", key)
		if err != nil {
			return adminauth.ErrAuthUnavailable
		}
		if tag.RowsAffected() == 1 {
			continue
		}
		var start, expiry, now time.Time
		var used int
		if err = tx.QueryRow(ctx, "SELECT started_at,expires_at,used FROM auth_login_buckets WHERE bucket_key=$1 FOR UPDATE", key).Scan(&start, &expiry, &used); err != nil {
			return adminauth.ErrAuthUnavailable
		}
		if err = tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
			return adminauth.ErrAuthUnavailable
		}
		if now.Before(start) || now.Before(expiry) && used >= limit {
			allowed = false
			break
		}
		if !now.Before(expiry) {
			_, err = tx.Exec(ctx, "UPDATE auth_login_buckets SET started_at=$2::timestamptz,expires_at=$2::timestamptz+interval '1 minute',used=1 WHERE bucket_key=$1", key, now)
		} else {
			_, err = tx.Exec(ctx, "UPDATE auth_login_buckets SET used=used+1 WHERE bucket_key=$1", key)
		}
		if err != nil {
			return adminauth.ErrAuthUnavailable
		}
	}
	if allowed {
		if _, err = tx.Exec(ctx, "DELETE FROM auth_login_buckets WHERE bucket_key LIKE 'http-%' AND bucket_key<>'http-installation' AND expires_at<=clock_timestamp()"); err != nil {
			return adminauth.ErrAuthUnavailable
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return adminauth.ErrAuthUnavailable
	}
	if !allowed {
		return ErrAuthHTTPRateLimit
	}
	return nil
}
