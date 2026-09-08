// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	_ "embed"
	"errors"
	"github.com/jackc/pgx/v5"
	"net/http"
	"time"
	"waiting-room/internal/adminauth"
)

//go:embed migrations/013_logout_replay.sql
var Migration013 string
var ErrLogoutConflict = errors.New("LOGOUT_IDEMPOTENCY_CONFLICT")
var ErrLogoutInvalid = errors.New("INVALID_LOGOUT_REQUEST")

// LogoutReplay supports a 24-hour acknowledgement after session deletion. The
// old token + original CSRF + key can only acknowledge this logout; they never
// authenticate another endpoint. Keyless legacy clients retain delete/401 behavior.
func (s *SessionService) LogoutReplay(ctx context.Context, token string, request *http.Request) (bool, error) {
	if request == nil || request.Method != "POST" {
		return false, adminauth.ErrForbidden
	}
	if len(request.Header.Values("Idempotency-Key")) == 0 {
		return false, s.Logout(ctx, token, request)
	}
	if len(request.Header.Values("Idempotency-Key")) != 1 || !safeCommandKey(request.Header.Get("Idempotency-Key")) {
		return false, ErrLogoutInvalid
	}
	hash, ok := tokenHash(token)
	if !ok {
		return false, adminauth.ErrUnauthenticated
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := authTx(ctx, s.store)
	if err != nil {
		return false, err
	}
	defer rollback(tx)
	key := sha256.Sum256([]byte(request.Header.Get("Idempotency-Key")))
	replay := func() (bool, error) {
		var csrfRaw, keyRaw []byte
		err := tx.QueryRow(ctx, "SELECT csrf_hash,key_hash FROM auth_logout_replays WHERE session_hash=$1 AND created_at<=clock_timestamp() AND expires_at>clock_timestamp()", hash[:]).Scan(&csrfRaw, &keyRaw)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		if err != nil || len(csrfRaw) != 32 {
			return false, adminauth.ErrAuthUnavailable
		}
		var csrf [32]byte
		copy(csrf[:], csrfRaw)
		if err = s.origin.CheckMutation(request, csrf); err != nil {
			return false, err
		}
		if subtle.ConstantTimeCompare(keyRaw, key[:]) != 1 {
			return false, ErrLogoutConflict
		}
		return true, nil
	}
	if found, e := replay(); found || e != nil {
		return found, e
	}
	state, err := loadSession(ctx, tx, hash, true)
	if err != nil {
		// A simultaneous successful logout may have deleted the row while we waited.
		if errors.Is(err, adminauth.ErrUnauthenticated) {
			if found, e := replay(); found || e != nil {
				return found, e
			}
		}
		return false, err
	}
	if err = s.origin.CheckMutation(request, state.csrf); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, "DELETE FROM auth_logout_replays WHERE session_hash IN (SELECT session_hash FROM auth_logout_replays WHERE expires_at<=clock_timestamp() ORDER BY expires_at LIMIT 128)"); err != nil {
		return false, adminauth.ErrAuthUnavailable
	}
	// Serialize capacity accounting, without serializing ordinary session reads.
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(71304013)"); err != nil {
		return false, adminauth.ErrAuthUnavailable
	}
	var count int
	if tx.QueryRow(ctx, "SELECT count(*) FROM auth_logout_replays").Scan(&count) != nil || count >= 10000 {
		return false, adminauth.ErrAuthUnavailable
	}
	if _, err = tx.Exec(ctx, "INSERT INTO auth_logout_replays(session_hash,csrf_hash,key_hash) VALUES($1,$2,$3)", hash[:], state.csrf[:], key[:]); err != nil {
		return false, adminauth.ErrAuthUnavailable
	}
	if _, err = tx.Exec(ctx, "DELETE FROM auth_sessions WHERE token_hash=$1", hash[:]); err != nil {
		return false, adminauth.ErrAuthUnavailable
	}
	if err = s.store.auditAuth(ctx, tx, state.account.ID, "auth.logout", "logged_out"); err != nil {
		return false, err
	}
	if tx.Commit(ctx) != nil {
		return false, adminauth.ErrAuthUnavailable
	}
	return false, nil
}
