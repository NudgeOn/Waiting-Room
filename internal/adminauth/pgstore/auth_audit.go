// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"context"
	"github.com/jackc/pgx/v5"
	"waiting-room/internal/adminauth"
)

// Values are fixed taxonomy, not request text. Failed password attempts use a
// system actor: no attempted username, source address, credential or hash is
// persisted in the audit. Known actors are loaded from the already-locked DB
// account for successful/credential-bound actions, never from a client role.
func (s *Store) auditAuth(ctx context.Context, tx pgx.Tx, user, action, result string) error {
	if s == nil || !s.authAudit {
		return nil
	}
	switch action {
	case "auth.bootstrap", "auth.login", "auth.lockout", "auth.logout", "auth.totp.enroll", "auth.totp.recover":
	default:
		return adminauth.ErrAuthUnavailable
	}
	switch result {
	case "admin_created", "authenticated", "rejected", "locked", "logged_out", "enrolled", "challenge_required", "enrollment_required":
	default:
		return adminauth.ErrAuthUnavailable
	}
	requestID, _, err := adminauth.NewCSRFToken()
	if err != nil {
		return adminauth.ErrAuthUnavailable
	}
	before := digest([]byte("wr-auth-audit/v1:transition"))
	after := digest([]byte("wr-auth-audit/v1:" + action + ":" + result))
	query := "INSERT INTO control_audit(actor_id,actor_role,action,target_id,before_digest,after_digest,result,request_id,revision) SELECT id,role,$2,'authentication',$3,$4,$5,$6,0 FROM auth_accounts WHERE id=$1"
	if user == "" {
		user = "system:authentication"
		query = "INSERT INTO control_audit(actor_id,actor_role,action,target_id,before_digest,after_digest,result,request_id,revision) VALUES($1,'system',$2,'authentication',$3,$4,$5,$6,0)"
	}
	tag, err := tx.Exec(ctx, query, user, action, before, after, result, requestID)
	if err != nil || tag.RowsAffected() != 1 {
		return adminauth.ErrAuthUnavailable
	}
	return nil
}

func (s *Store) auditRejectedPassword(ctx context.Context) error {
	if !s.authAudit {
		return nil
	}
	tx, err := authTx(ctx, s)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if err = s.auditAuth(ctx, tx, "", "auth.login", "rejected"); err != nil {
		return err
	}
	if tx.Commit(ctx) != nil {
		return adminauth.ErrAuthUnavailable
	}
	return nil
}
