// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"waiting-room/internal/adminauth"
	"waiting-room/internal/control"
)

type commandResult struct {
	reply         ControlReply
	before, after string
	revision      int64
}
type commandBody func(context.Context, pgx.Tx, sessionSnapshot, control.Config) (commandResult, error)

// command serializes all operator mutations behind the config row. Callbacks
// perform DB work and bounded local computation only, never DNS/network/KDF.
func (s *ControlService) command(ctx context.Context, token string, r *http.Request, raw []byte, action adminauth.Action, target string, etag bool, body commandBody) (ControlReply, error) {
	if r == nil || len(raw) > control.MaxConfigBytes || len(r.Header.Values("Idempotency-Key")) != 1 || !safeCommandKey(r.Header.Get("Idempotency-Key")) {
		return replyProblem(400, "INVALID_REQUEST"), nil
	}
	if etag && len(r.Header.Values("If-Match")) == 0 {
		return replyProblem(428, "PRECONDITION_REQUIRED"), nil
	}
	if len(r.Header.Values("If-Match")) > 1 || len(r.Header.Get("If-Match")) > 64 {
		return replyProblem(400, "INVALID_REQUEST"), nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, state, err := s.begin(ctx, token, action, r)
	if err != nil {
		return ControlReply{}, err
	}
	defer rollback(tx)
	current, err := readConfig(ctx, tx, true)
	if err != nil {
		return ControlReply{}, err
	}
	if tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&state.now) != nil {
		return ControlReply{}, adminauth.ErrAuthUnavailable
	}
	if err = adminauth.CheckSession(state.account, state.session, state.policy, state.now); err != nil {
		return ControlReply{}, err
	}
	key := sha256.Sum256([]byte(r.Header.Get("Idempotency-Key")))
	fingerprint := sha256.Sum256(append([]byte("control.command.v1\x00"+r.Method+"\x00"+string(action)+"\x00"+target+"\x00"+r.Header.Get("If-Match")+"\x00"), raw...))
	var old, bytes []byte
	var created, expires time.Time
	var reply ControlReply
	err = tx.QueryRow(ctx, "SELECT request_hash,created_at,expires_at,status,response,etag FROM control_commands WHERE actor_id=$1 AND key_hash=$2", state.account.ID, key[:]).Scan(&old, &created, &expires, &reply.Status, &bytes, &reply.ETag)
	if err == nil {
		if state.now.Before(created) {
			return ControlReply{}, adminauth.ErrAuthUnavailable
		}
		if state.now.Before(expires) {
			if string(old) != string(fingerprint[:]) {
				return replyProblem(409, "IDEMPOTENCY_CONFLICT"), nil
			}
			if tx.Commit(ctx) != nil {
				return ControlReply{}, adminauth.ErrAuthUnavailable
			}
			reply.Body = bytes
			reply.Replay = true
			return reply, nil
		}
		if _, err = tx.Exec(ctx, "DELETE FROM control_commands WHERE actor_id=$1 AND key_hash=$2", state.account.ID, key[:]); err != nil {
			return ControlReply{}, adminauth.ErrAuthUnavailable
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return ControlReply{}, adminauth.ErrAuthUnavailable
	}
	if _, err = tx.Exec(ctx, "DELETE FROM control_commands WHERE (actor_id,key_hash) IN (SELECT actor_id,key_hash FROM control_commands WHERE expires_at<=$1 ORDER BY expires_at LIMIT 128)", state.now); err != nil {
		return ControlReply{}, adminauth.ErrAuthUnavailable
	}
	var count int
	if tx.QueryRow(ctx, "SELECT count(*) FROM control_commands").Scan(&count) != nil || count >= maxControlCommands {
		return replyProblem(503, "COMMAND_CAPACITY_EXCEEDED"), nil
	}
	result, err := body(ctx, tx, state, current)
	if err != nil {
		return ControlReply{}, err
	}
	if result.before == "" {
		result.before = digest(current.Bytes())
	}
	if result.after == "" {
		result.after = result.before
	}
	if result.revision == 0 {
		result.revision = current.Revision
	}
	if result.reply.Status < 200 || len(result.reply.Body) == 0 || len(result.reply.Body) > control.MaxConfigBytes {
		return ControlReply{}, adminauth.ErrAuthUnavailable
	}
	requestID, _, err := adminauth.NewCSRFToken()
	if err != nil {
		return ControlReply{}, adminauth.ErrAuthUnavailable
	}
	outcome := "accepted"
	if result.reply.Status >= 400 {
		outcome = "rejected"
	}
	if _, err = tx.Exec(ctx, "INSERT INTO control_audit(actor_id,actor_role,action,target_id,before_digest,after_digest,result,request_id,revision) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)", state.account.ID, state.account.Role, string(action), target, result.before, result.after, outcome, requestID, result.revision); err != nil {
		return ControlReply{}, adminauth.ErrAuthUnavailable
	}
	if _, err = tx.Exec(ctx, "INSERT INTO control_commands(actor_id,key_hash,request_hash,created_at,expires_at,status,response,etag) VALUES($1,$2,$3,$4::timestamptz,$4::timestamptz+interval '24 hours',$5,$6,$7)", state.account.ID, key[:], fingerprint[:], state.now, result.reply.Status, result.reply.Body, result.reply.ETag); err != nil {
		return ControlReply{}, adminauth.ErrAuthUnavailable
	}
	if _, err = tx.Exec(ctx, "UPDATE auth_sessions SET last_seen_at=$1 WHERE token_hash=$2", state.now, mustSessionHash(token)); err != nil {
		return ControlReply{}, adminauth.ErrAuthUnavailable
	}
	if tx.Commit(ctx) != nil {
		return ControlReply{}, adminauth.ErrAuthUnavailable
	}
	return result.reply, nil
}
