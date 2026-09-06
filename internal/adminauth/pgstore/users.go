// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"waiting-room/internal/adminauth"
	"waiting-room/internal/control"
)

//go:embed migrations/008_account_management.sql
var Migration008 string

type UserView struct {
	ID           string         `json:"id"`
	Role         adminauth.Role `json:"role"`
	Enabled      bool           `json:"enabled"`
	TOTPEnrolled bool           `json:"totpEnrolled"`
	Revision     uint64         `json:"revision"`
	ETag         string         `json:"etag"`
}

func userETag(version uint64) string { return fmt.Sprintf(`"user-%d"`, version) }

type CreateUserInput struct {
	ID       string         `json:"id"`
	Role     adminauth.Role `json:"role"`
	Password string         `json:"password"`
}

func (CreateUserInput) String() string               { return "[REDACTED_CREATE_USER]" }
func (CreateUserInput) GoString() string             { return "[REDACTED_CREATE_USER]" }
func (CreateUserInput) MarshalJSON() ([]byte, error) { return json.Marshal("[REDACTED_CREATE_USER]") }

type UpdateUserInput struct {
	Role    adminauth.Role `json:"role"`
	Enabled *bool          `json:"enabled"`
}

func validRole(role adminauth.Role) bool {
	return role == adminauth.Admin || role == adminauth.Operator || role == adminauth.Viewer
}

func (s *SecurityService) Users(ctx context.Context, token string) (ControlReply, error) {
	tx, _, err := s.control.begin(ctx, token, adminauth.ReadUsers, nil)
	if err != nil {
		return ControlReply{}, err
	}
	defer rollback(tx)
	rows, err := tx.Query(ctx, `SELECT a.id,a.role,a.enabled,a.session_version,EXISTS(SELECT 1 FROM auth_totp_credentials c WHERE c.user_id=a.id) FROM auth_accounts a WHERE NOT a.deleted ORDER BY a.id LIMIT 1001`)
	if err != nil {
		return ControlReply{}, adminauth.ErrAuthUnavailable
	}
	users := []UserView{}
	for rows.Next() {
		var u UserView
		if rows.Scan(&u.ID, &u.Role, &u.Enabled, &u.Revision, &u.TOTPEnrolled) != nil {
			rows.Close()
			return ControlReply{}, adminauth.ErrAuthUnavailable
		}
		u.ETag = userETag(u.Revision)
		users = append(users, u)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(users) > 1000 || tx.Commit(ctx) != nil {
		return ControlReply{}, adminauth.ErrAuthUnavailable
	}
	return jsonReply(200, map[string]any{"users": users}, ""), nil
}

func (s *SecurityService) CreateUser(ctx context.Context, token string, r *http.Request, raw []byte) (ControlReply, error) {
	var in CreateUserInput
	if r == nil || r.Method != "POST" || control.DecodeExact(raw, &in) != nil || !safeID.MatchString(in.ID) || !validRole(in.Role) || !adminauth.ValidLoginPassword(in.Password) || utf8.RuneCountInString(in.Password) < 15 {
		return replyProblem(400, "INVALID_REQUEST"), nil
	}
	// Check current authority and the action-bound proof BEFORE expensive work.
	// Rollback deliberately leaves the proof available for the final command.
	tx, _, err := s.control.beginCommand(ctx, token, adminauth.WriteUsers, r, true)
	if err != nil {
		return ControlReply{}, err
	}
	err = consumeReauth(ctx, tx, token, r, adminauth.WriteUsers, in.ID, raw)
	rollback(tx)
	if err != nil {
		// A previously committed create has consumed its proof. Still allow the
		// normal durable replay lookup, but never perform a new write here.
		return s.control.command(ctx, token, r, raw, adminauth.WriteUsers, in.ID, false, func(context.Context, pgx.Tx, sessionSnapshot, control.Config) (commandResult, error) {
			return commandResult{}, adminauth.ErrForbidden
		})
	}
	hash, err := s.login.passwords.Hash(ctx, in.Password)
	if err != nil {
		return ControlReply{}, err
	}
	return s.control.command(ctx, token, r, raw, adminauth.WriteUsers, in.ID, false, func(ctx context.Context, tx pgx.Tx, state sessionSnapshot, _ control.Config) (commandResult, error) {
		var exists, complete bool
		var count int
		if tx.QueryRow(ctx, "SELECT completed FROM auth_bootstrap WHERE singleton").Scan(&complete) != nil || !complete {
			return commandResult{}, adminauth.ErrForbidden
		}
		if tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM auth_accounts WHERE id=$1), (SELECT count(*) FROM auth_accounts)", in.ID).Scan(&exists, &count) != nil {
			return commandResult{}, adminauth.ErrAuthUnavailable
		}
		if exists {
			return commandResult{reply: replyProblem(409, "USER_ID_RESERVED")}, nil
		}
		if count >= 1000 {
			return commandResult{reply: replyProblem(409, "USER_CAPACITY_EXCEEDED")}, nil
		}
		if _, err := tx.Exec(ctx, "INSERT INTO auth_accounts(id,role) VALUES($1,$2)", in.ID, in.Role); err != nil {
			return commandResult{}, adminauth.ErrAuthUnavailable
		}
		if _, err := tx.Exec(ctx, "INSERT INTO auth_password_credentials VALUES($1,1,$2)", in.ID, hash.StorageValue()); err != nil {
			return commandResult{}, adminauth.ErrAuthUnavailable
		}
		u := UserView{ID: in.ID, Role: in.Role, Enabled: true, Revision: 1, ETag: userETag(1)}
		return userResult(u, 201), nil
	})
}

func userResult(u UserView, status int) commandResult {
	b, _ := json.Marshal(u)
	return commandResult{reply: jsonReply(status, u, u.ETag), after: digest(b), revision: int64(u.Revision)}
}

// Revoke all authentication paths, not just dashboard sessions. Password remains
// available for a fresh TOTP enrollment unless this is account deletion.
func revokeUser(ctx context.Context, tx pgx.Tx, id string) error {
	for _, table := range []string{"auth_sessions", "auth_totp_challenges", "auth_enrollment_challenges"} {
		if _, err := tx.Exec(ctx, "DELETE FROM "+table+" WHERE user_id=$1", id); err != nil {
			return adminauth.ErrAuthUnavailable
		}
	}
	return nil
}
func removeTOTP(ctx context.Context, tx pgx.Tx, id string) error {
	for _, table := range []string{"auth_recovery_codes", "auth_totp_credentials"} {
		if _, err := tx.Exec(ctx, "DELETE FROM "+table+" WHERE user_id=$1", id); err != nil {
			return adminauth.ErrAuthUnavailable
		}
	}
	return nil
}
func (s *SecurityService) ChangeUser(ctx context.Context, token, id string, r *http.Request, raw []byte, reset bool) (ControlReply, error) {
	var in UpdateUserInput
	if r == nil || !safeID.MatchString(id) {
		return replyProblem(400, "INVALID_REQUEST"), nil
	}
	action := adminauth.WriteUsers
	if reset {
		action = adminauth.ResetUserTOTP
	}
	if (reset && (r.Method != "POST" || control.DecodeExact(raw, &struct{}{}) != nil)) || (!reset && r.Method != "DELETE" && (r.Method != "PATCH" || control.DecodeExact(raw, &in) != nil || !validRole(in.Role) || in.Enabled == nil)) || (r.Method == "DELETE" && len(raw) != 0) {
		return replyProblem(400, "INVALID_REQUEST"), nil
	}
	return s.control.command(ctx, token, r, raw, action, id, true, func(ctx context.Context, tx pgx.Tx, state sessionSnapshot, _ control.Config) (commandResult, error) {
		var u UserView
		err := tx.QueryRow(ctx, "SELECT id,role,enabled,session_version,EXISTS(SELECT 1 FROM auth_totp_credentials WHERE user_id=$1) FROM auth_accounts WHERE id=$1 AND NOT deleted FOR UPDATE", id).Scan(&u.ID, &u.Role, &u.Enabled, &u.Revision, &u.TOTPEnrolled)
		if errors.Is(err, pgx.ErrNoRows) {
			return commandResult{reply: replyProblem(404, "NOT_FOUND")}, nil
		}
		if err != nil {
			return commandResult{}, adminauth.ErrAuthUnavailable
		}
		u.ETag = userETag(u.Revision)
		before, _ := json.Marshal(u)
		if r.Header.Get("If-Match") != u.ETag {
			return commandResult{reply: replyProblem(412, "REVISION_MISMATCH")}, nil
		}
		destructive := r.Method == "DELETE" || (!reset && (!*in.Enabled || in.Role != adminauth.Admin))
		if u.Role == adminauth.Admin && u.Enabled && (destructive || (reset && state.policy.TOTPEnabled)) {
			var others int
			if tx.QueryRow(ctx, "SELECT count(*) FROM auth_accounts WHERE role='admin' AND enabled AND NOT deleted AND id<>$1", id).Scan(&others) != nil {
				return commandResult{}, adminauth.ErrAuthUnavailable
			}
			if others == 0 {
				return commandResult{reply: replyProblem(409, "LAST_ADMIN_REQUIRED")}, nil
			}
		}
		if u.Revision >= 9007199254740990 {
			return commandResult{}, adminauth.ErrAuthUnavailable
		}
		if err = revokeUser(ctx, tx, id); err != nil {
			return commandResult{}, err
		}
		if reset || r.Method == "DELETE" {
			if err = removeTOTP(ctx, tx, id); err != nil {
				return commandResult{}, err
			}
			u.TOTPEnrolled = false
		}
		if r.Method == "DELETE" {
			if _, err = tx.Exec(ctx, "DELETE FROM auth_password_credentials WHERE user_id=$1", id); err != nil {
				return commandResult{}, adminauth.ErrAuthUnavailable
			}
			u.Enabled = false
		} else if !reset {
			u.Role = in.Role
			u.Enabled = *in.Enabled
		}
		u.Revision++
		u.ETag = userETag(u.Revision)
		if _, err = tx.Exec(ctx, "UPDATE auth_accounts SET role=$2,enabled=$3,session_version=$4,deleted=$5 WHERE id=$1", id, u.Role, u.Enabled, u.Revision, r.Method == "DELETE"); err != nil {
			return commandResult{}, adminauth.ErrAuthUnavailable
		}
		result := userResult(u, 200)
		result.before = digest(before)
		return result, nil
	})
}
