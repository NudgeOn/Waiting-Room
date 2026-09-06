// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"waiting-room/internal/adminauth"
)

// SessionView is display data, never a reusable authorization proof.
type SessionView struct {
	UserID            string                 `json:"userId"`
	Role              adminauth.Role         `json:"role"`
	MFAVerified       bool                   `json:"mfaVerified"`
	TOTPEnrolled      bool                   `json:"totpEnrolled"`
	CapabilityVersion int                    `json:"capabilityVersion"`
	Capabilities      []adminauth.Capability `json:"capabilities"`
	CreatedAt         time.Time              `json:"createdAt"`
	LastSeenAt        time.Time              `json:"lastSeenAt"`
	IdleExpiresAt     time.Time              `json:"idleExpiresAt"`
	AbsoluteExpiresAt time.Time              `json:"absoluteExpiresAt"`
}
type SessionService struct {
	store  *Store
	origin adminauth.OriginPolicy
}

func NewSessionService(store *Store, origin string, allowLoopbackHTTP bool) (*SessionService, error) {
	p, err := adminauth.NewOriginPolicy(origin, allowLoopbackHTTP)
	if store == nil || err != nil {
		return nil, adminauth.ErrAuthUnavailable
	}
	return &SessionService{store, p}, nil
}

type sessionSnapshot struct {
	account adminauth.Account
	session adminauth.Session
	policy  adminauth.AuthPolicy
	csrf    [32]byte
	now     time.Time
}

func (s sessionSnapshot) view() SessionView {
	absolute := s.session.CreatedAt.Add(adminauth.SessionAbsoluteTTL)
	idle := s.session.LastSeenAt.Add(adminauth.SessionIdleTTL)
	if absolute.Before(idle) {
		idle = absolute
	}
	return SessionView{s.account.ID, s.account.Role, s.session.MFAVerified, s.account.TOTPEnrolled,
		adminauth.CapabilityVersion, adminauth.Capabilities(s.account.Role), s.session.CreatedAt,
		s.session.LastSeenAt, idle, absolute}
}

// Lock order: policy -> account -> optional credential -> session. Reads share-lock
// state, mutations take an exclusive session lock. No lock is upgraded in place.
func loadSession(ctx context.Context, tx pgx.Tx, hash [32]byte, mutate bool) (sessionSnapshot, error) {
	var out sessionSnapshot
	err := tx.QueryRow(ctx, "SELECT mode,totp_enabled,version FROM auth_policy WHERE singleton FOR SHARE").Scan(&out.policy.Mode, &out.policy.TOTPEnabled, &out.policy.Version)
	if err != nil || out.policy.Validate() != nil {
		return out, adminauth.ErrAuthUnavailable
	}
	var user string
	err = tx.QueryRow(ctx, "SELECT user_id FROM auth_sessions WHERE token_hash=$1", hash[:]).Scan(&user)
	if err != nil {
		return out, loginDBError(err)
	}
	err = tx.QueryRow(ctx, "SELECT id,role,enabled,session_version FROM auth_accounts WHERE id=$1 FOR SHARE", user).Scan(&out.account.ID, &out.account.Role, &out.account.Enabled, &out.account.SessionVersion)
	if err != nil {
		return out, loginDBError(err)
	}
	var version uint64
	err = tx.QueryRow(ctx, "SELECT version FROM auth_totp_credentials WHERE user_id=$1 FOR SHARE", user).Scan(&version)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return out, adminauth.ErrAuthUnavailable
	}
	out.account.TOTPEnrolled = err == nil
	lock := " FOR SHARE"
	if mutate {
		lock = " FOR UPDATE"
	}
	var csrf []byte
	err = tx.QueryRow(ctx, "SELECT user_id,policy_version,user_version,mfa_verified,created_at,last_seen_at,csrf_hash FROM auth_sessions WHERE token_hash=$1"+lock, hash[:]).Scan(&out.session.UserID, &out.session.AuthPolicyVersion, &out.session.UserSessionVersion, &out.session.MFAVerified, &out.session.CreatedAt, &out.session.LastSeenAt, &csrf)
	if err != nil {
		return out, loginDBError(err)
	}
	if len(csrf) != 32 {
		return out, adminauth.ErrAuthUnavailable
	}
	copy(out.csrf[:], csrf)
	if err = tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&out.now); err != nil {
		return out, adminauth.ErrAuthUnavailable
	}
	if err = adminauth.CheckSession(out.account, out.session, out.policy, out.now); err != nil {
		return out, err
	}
	return out, nil
}

// Me never refreshes idle TTL; every call reads current authoritative DB state.
func (s *SessionService) Me(ctx context.Context, token string) (SessionView, error) {
	return s.run(ctx, token, nil, "read")
}

// Touch is an internal activity operation, not yet a public Admin endpoint.
func (s *SessionService) Touch(ctx context.Context, token string, request *http.Request) (SessionView, error) {
	return s.run(ctx, token, request, "touch")
}
func (s *SessionService) Logout(ctx context.Context, token string, request *http.Request) error {
	_, err := s.run(ctx, token, request, "logout")
	return err
}
func (s *SessionService) run(ctx context.Context, token string, request *http.Request, operation string) (SessionView, error) {
	hash, ok := tokenHash(token)
	if !ok {
		return SessionView{}, adminauth.ErrUnauthenticated
	}
	mutate := operation != "read"
	if mutate && (request == nil || request.Method != http.MethodPost) {
		return SessionView{}, adminauth.ErrForbidden
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := authTx(ctx, s.store)
	if err != nil {
		return SessionView{}, err
	}
	defer rollback(tx)
	state, err := loadSession(ctx, tx, hash, mutate)
	if err != nil {
		return SessionView{}, err
	}
	if mutate {
		if err = s.origin.CheckMutation(request, state.csrf); err != nil {
			return SessionView{}, err
		}
		if operation == "logout" {
			_, err = tx.Exec(ctx, "DELETE FROM auth_sessions WHERE token_hash=$1", hash[:])
			if err == nil {
				err = s.store.auditAuth(ctx, tx, state.account.ID, "auth.logout", "logged_out")
			}
		} else {
			// Re-read wall clock at the actual update, never revive an expired session.
			err = tx.QueryRow(ctx, "UPDATE auth_sessions SET last_seen_at=stamp.t FROM (SELECT clock_timestamp() AS t) stamp WHERE token_hash=$1 AND created_at<=stamp.t AND last_seen_at<=stamp.t AND created_at+interval '8 hours'>stamp.t AND last_seen_at+interval '30 minutes'>stamp.t RETURNING last_seen_at", hash[:]).Scan(&state.session.LastSeenAt)
		}
		if err != nil {
			return SessionView{}, loginDBError(err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return SessionView{}, adminauth.ErrAuthUnavailable
	}
	return state.view(), nil
}
