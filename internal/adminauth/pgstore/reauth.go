// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"context"
	_ "embed"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"math"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"time"
	"waiting-room/internal/adminauth"
	"waiting-room/internal/control"
)

//go:embed migrations/007_reauthentication.sql
var Migration007 string

type ReauthInput struct {
	Password      string           `json:"password"`
	TOTP          string           `json:"totp,omitempty"`
	Action        adminauth.Action `json:"action"`
	TargetID      string           `json:"targetId"`
	RequestDigest string           `json:"requestDigest"`
}

func (ReauthInput) String() string               { return "[REDACTED_REAUTH_INPUT]" }
func (ReauthInput) GoString() string             { return "[REDACTED_REAUTH_INPUT]" }
func (ReauthInput) MarshalJSON() ([]byte, error) { return json.Marshal("[REDACTED_REAUTH_INPUT]") }

type SecurityService struct {
	control *ControlService
	login   *LoginService
	keyID   string
}

func NewSecurityService(s *Store, p *adminauth.PasswordHasher, key [32]byte, origin string, activeKeyID ...string) (*SecurityService, error) {
	c, e := NewControlService(s, origin)
	if e != nil {
		return nil, e
	}
	l, e := NewLoginService(s, p, key)
	if e != nil {
		return nil, e
	}
	kid := ""
	if len(activeKeyID) > 1 {
		return nil, adminauth.ErrAuthUnavailable
	}
	if len(activeKeyID) == 1 {
		kid = activeKeyID[0]
	} else if len(s.vault.keys) == 1 {
		for id := range s.vault.keys {
			kid = id
		}
	}
	if s.vault.keys[kid] == nil {
		return nil, adminauth.ErrAuthUnavailable
	}
	return &SecurityService{c, l, kid}, nil
}

// ActionRequestDigest binds exact UTF-8 request bytes as well as method, resource
// and optimistic revision. Callers must submit the same bytes after reauth.
func ActionRequestDigest(method, target, etag string, raw []byte) string {
	return digest(append([]byte("wr-action/v1\n"+method+"\n"+target+"\n"+etag+"\n"), raw...))
}
func (s *SecurityService) Reauthenticate(ctx context.Context, token string, r *http.Request, raw []byte) (ControlReply, error) {
	var in ReauthInput
	if r == nil || r.Method != "POST" || control.DecodeExact(raw, &in) != nil || !adminauth.ValidLoginPassword(in.Password) || !safeID.MatchString(in.TargetID) || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(in.RequestDigest) {
		return replyProblem(400, "INVALID_REQUEST"), nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, state, err := s.control.begin(ctx, token, adminauth.ReadDashboard, r)
	if err != nil {
		return ControlReply{}, err
	}
	req, allowed := adminauth.Requirement(state.account.Role, in.Action)
	if !allowed || !req.RequiresReauthentication || in.Action == adminauth.RotateKeys {
		rollback(tx)
		return ControlReply{}, adminauth.ErrForbidden
	}
	if tx.Commit(ctx) != nil {
		return ControlReply{}, adminauth.ErrAuthUnavailable
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return ControlReply{}, adminauth.ErrForbidden
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return ControlReply{}, adminauth.ErrForbidden
	}
	source, ok := sourceValue(peer)
	if !ok {
		return ControlReply{}, adminauth.ErrForbidden
	}
	if err = s.login.reserveAttempt(ctx, state.account.ID, source); err != nil {
		return ControlReply{}, err
	}
	before, err := s.login.snapshot(ctx, state.account.ID)
	if err != nil {
		return ControlReply{}, adminauth.ErrUnauthenticated
	}
	valid, err := s.login.passwords.Verify(ctx, in.Password, before.hash)
	if err != nil {
		return ControlReply{}, err
	}
	if !valid {
		return ControlReply{}, adminauth.ErrUnauthenticated
	}
	// No hashing under row locks. Exclusive policy lock precedes all account,
	// credential and session locks, preventing credential lock upgrade deadlocks.
	tx, err = authTx(ctx, s.control.store)
	if err != nil {
		return ControlReply{}, err
	}
	defer rollback(tx)
	if _, err = tx.Exec(ctx, "SELECT singleton FROM auth_policy WHERE singleton FOR UPDATE"); err != nil {
		return ControlReply{}, adminauth.ErrAuthUnavailable
	}
	hash, _ := tokenHash(token)
	state, err = loadSession(ctx, tx, hash, true)
	if err != nil {
		return ControlReply{}, err
	}
	if err = s.control.origin.CheckMutation(r, state.csrf); err != nil {
		return ControlReply{}, err
	}
	req, allowed = adminauth.Requirement(state.account.Role, in.Action)
	if !allowed || !req.RequiresReauthentication || state.account.ID != before.user || state.account.SessionVersion != before.userVersion || state.policy.Version != before.policyVersion {
		return ControlReply{}, adminauth.ErrForbidden
	}
	var passwordHash string
	var version uint64
	if tx.QueryRow(ctx, "SELECT version,password_hash FROM auth_password_credentials WHERE user_id=$1 FOR SHARE", state.account.ID).Scan(&version, &passwordHash) != nil || version != before.passwordVersion || passwordHash != before.hash {
		return ControlReply{}, adminauth.ErrUnauthenticated
	}
	if state.policy.TOTPEnabled || (in.Action == adminauth.WriteTOTPPolicy && in.TargetID == "totp") {
		var ref adminauth.CredentialRef
		ref.UserID = state.account.ID
		var kid string
		var sealed []byte
		if tx.QueryRow(ctx, "SELECT version,key_id,sealed_secret FROM auth_totp_credentials WHERE user_id=$1 FOR UPDATE", ref.UserID).Scan(&ref.Version, &kid, &sealed) != nil {
			return ControlReply{}, adminauth.ErrUnauthenticated
		}
		secret, e := s.control.store.vault.open(ref, kid, sealed)
		if e != nil {
			return ControlReply{}, adminauth.ErrAuthUnavailable
		}
		if e = adminauth.VerifyAndConsume(ctx, ref, secret, in.TOTP, state.now, reauthCounter{tx, ref}); e != nil {
			return ControlReply{}, e
		}
	}
	proof, proofHash, err := adminauth.NewCSRFToken()
	if err != nil {
		return ControlReply{}, err
	}
	if _, err = tx.Exec(ctx, "DELETE FROM auth_reauth_proofs WHERE token_hash IN (SELECT token_hash FROM auth_reauth_proofs WHERE expires_at<=clock_timestamp() ORDER BY expires_at LIMIT 128)"); err != nil {
		return ControlReply{}, adminauth.ErrAuthUnavailable
	}
	var count int
	if tx.QueryRow(ctx, "SELECT count(*) FROM auth_reauth_proofs").Scan(&count) != nil || count >= 1000 {
		return ControlReply{}, adminauth.ErrAuthUnavailable
	}
	expires := state.now.Add(5 * time.Minute)
	if _, err = tx.Exec(ctx, "INSERT INTO auth_reauth_proofs(token_hash,session_hash,action,target_id,request_digest,created_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7)", proofHash[:], hash[:], in.Action, in.TargetID, in.RequestDigest, state.now, expires); err != nil {
		return ControlReply{}, adminauth.ErrAuthUnavailable
	}
	id, _, err := adminauth.NewCSRFToken()
	if err != nil {
		return ControlReply{}, err
	}
	if _, err = tx.Exec(ctx, "INSERT INTO control_audit(actor_id,actor_role,action,target_id,before_digest,after_digest,result,request_id,revision) VALUES($1,$2,'auth.reauth',$3,$4,$4,'accepted',$5,0)", state.account.ID, state.account.Role, in.TargetID, in.RequestDigest, id); err != nil {
		return ControlReply{}, adminauth.ErrAuthUnavailable
	}
	if tx.Commit(ctx) != nil {
		return ControlReply{}, adminauth.ErrAuthUnavailable
	}
	return jsonReply(200, map[string]any{"reauthToken": proof, "expiresAt": expires}, ""), nil
}

type reauthCounter struct {
	tx  pgx.Tx
	ref adminauth.CredentialRef
}

func (c reauthCounter) ConsumeCounter(ctx context.Context, ref adminauth.CredentialRef, counter uint64) (bool, error) {
	if c.ref != ref || counter > math.MaxInt64 {
		return false, nil
	}
	tag, err := c.tx.Exec(ctx, "UPDATE auth_totp_credentials SET last_counter=$1 WHERE user_id=$2 AND version=$3 AND last_counter<$1", int64(counter), ref.UserID, ref.Version)
	return err == nil && tag.RowsAffected() == 1, err
}
func consumeReauth(ctx context.Context, tx pgx.Tx, token string, r *http.Request, action adminauth.Action, target string, raw []byte) error {
	if len(r.Header.Values("X-Reauth-Token")) != 1 {
		return adminauth.ErrForbidden
	}
	hash, ok := tokenHash(r.Header.Get("X-Reauth-Token"))
	if !ok {
		return adminauth.ErrForbidden
	}
	session, _ := tokenHash(token)
	tag, err := tx.Exec(ctx, "UPDATE auth_reauth_proofs SET consumed=true WHERE token_hash=$1 AND session_hash=$2 AND action=$3 AND target_id=$4 AND request_digest=$5 AND NOT consumed AND created_at<=clock_timestamp() AND expires_at>clock_timestamp()", hash[:], session[:], action, target, ActionRequestDigest(r.Method, target, r.Header.Get("If-Match"), raw))
	if err != nil {
		return adminauth.ErrAuthUnavailable
	}
	if tag.RowsAffected() != 1 {
		return adminauth.ErrForbidden
	}
	return nil
}
