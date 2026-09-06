// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"waiting-room/internal/adminauth"
	"waiting-room/internal/control"
)

//go:embed migrations/005_control.sql
var Migration005 string

const maxControlCommands = 10000

// ControlService persists operator drafts only. A separate signed publish/ACK
// pipeline must activate them; a successful save is NOT a runtime update.
type ControlService struct {
	store  *Store
	origin adminauth.OriginPolicy
}
type ControlReply struct {
	Status int
	Body   []byte
	ETag   string
	Replay bool
}
type AuditEvent struct {
	ID           int64          `json:"id"`
	OccurredAt   time.Time      `json:"occurredAt"`
	ActorID      string         `json:"actorId"`
	ActorRole    adminauth.Role `json:"actorRole"`
	Action       string         `json:"action"`
	TargetID     string         `json:"targetId"`
	BeforeDigest string         `json:"beforeDigest"`
	AfterDigest  string         `json:"afterDigest"`
	Result       string         `json:"result"`
	RequestID    string         `json:"requestId"`
	Revision     int64          `json:"revision"`
}

func NewControlService(store *Store, origin string) (*ControlService, error) {
	p, e := adminauth.NewOriginPolicy(origin, false)
	if store == nil || e != nil {
		return nil, adminauth.ErrAuthUnavailable
	}
	return &ControlService{store, p}, nil
}

// InitializeControl is deployment/migration-only and never overwrites saved data.
// No constructor or HTTP request calls this implicitly.
func (s *Store) InitializeControl(ctx context.Context, profile, region string) error {
	c := control.Config{SchemaVersion: 1, Profile: profile, RegionID: region, Rooms: []control.Room{}}
	if c.Validate() != nil {
		return control.ErrInvalid
	}
	_, e := s.pool.Exec(ctx, "INSERT INTO control_config(singleton,revision,document) VALUES(true,0,$1)", c.Bytes())
	if e != nil {
		return adminauth.ErrAuthUnavailable
	}
	return nil
}
func configETag(revision int64) string { return `"config-` + strconv.FormatInt(revision, 10) + `"` }
func safeCommandKey(key string) bool {
	if len(key) < 16 || len(key) > 128 {
		return false
	}
	for _, c := range key {
		if c < 33 || c > 126 {
			return false
		}
	}
	return true
}
func digest(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }
func replyProblem(status int, code string) ControlReply {
	id, _, _ := adminauth.NewCSRFToken()
	b, _ := json.Marshal(map[string]any{"type": "about:blank", "title": http.StatusText(status), "code": code, "status": status, "requestId": id})
	return ControlReply{Status: status, Body: b}
}
func (s *ControlService) begin(ctx context.Context, token string, action adminauth.Action, request *http.Request) (pgx.Tx, sessionSnapshot, error) {
	hash, ok := tokenHash(token)
	if !ok {
		return nil, sessionSnapshot{}, adminauth.ErrUnauthenticated
	}
	tx, e := authTx(ctx, s.store)
	if e != nil {
		return nil, sessionSnapshot{}, e
	}
	state, e := loadSession(ctx, tx, hash, request != nil)
	if e == nil {
		req, allowed := adminauth.Requirement(state.account.Role, action)
		if !allowed || req.RequiresReauthentication {
			e = adminauth.ErrForbidden
		}
	}
	if e == nil && request != nil {
		e = s.origin.CheckMutation(request, state.csrf)
	}
	if e != nil {
		rollback(tx)
		return nil, sessionSnapshot{}, e
	}
	return tx, state, nil
}
func readConfig(ctx context.Context, tx pgx.Tx, lock bool) (control.Config, error) {
	q := "SELECT revision,document FROM control_config WHERE singleton"
	if lock {
		q += " FOR UPDATE"
	}
	var revision int64
	var raw []byte
	var c control.Config
	if e := tx.QueryRow(ctx, q).Scan(&revision, &raw); e != nil {
		return c, adminauth.ErrAuthUnavailable
	}
	if control.DecodeExact(raw, &c) != nil || c.Validate() != nil || c.Revision != revision {
		return c, adminauth.ErrAuthUnavailable
	}
	return c, nil
}
func (s *ControlService) Config(ctx context.Context, token string) (ControlReply, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, _, e := s.begin(ctx, token, adminauth.ReadConfig, nil)
	if e != nil {
		return ControlReply{}, e
	}
	defer rollback(tx)
	c, e := readConfig(ctx, tx, false)
	if e != nil {
		return ControlReply{}, e
	}
	if tx.Commit(ctx) != nil {
		return ControlReply{}, adminauth.ErrAuthUnavailable
	}
	return ControlReply{Status: 200, Body: c.Bytes(), ETag: configETag(c.Revision)}, nil
}

// ReplaceDraft atomically validates current authority, revision, durable replay
// and audit. Lock order: auth policy/account/credential/session -> control config.
// No Valkey, HTTP, DNS, signer or other external call may run inside this transaction.
func (s *ControlService) ReplaceDraft(ctx context.Context, token string, r *http.Request, raw []byte) (ControlReply, error) {
	if r == nil || r.Method != "PUT" || len(raw) > control.MaxConfigBytes {
		return replyProblem(400, "INVALID_REQUEST"), nil
	}
	if len(r.Header.Values("Idempotency-Key")) != 1 || !safeCommandKey(r.Header.Get("Idempotency-Key")) {
		return replyProblem(400, "INVALID_REQUEST"), nil
	}
	if len(r.Header.Values("If-Match")) == 0 {
		return replyProblem(428, "PRECONDITION_REQUIRED"), nil
	}
	if len(r.Header.Values("If-Match")) != 1 || len(r.Header.Get("If-Match")) > 64 {
		return replyProblem(400, "INVALID_REQUEST"), nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, state, e := s.begin(ctx, token, adminauth.WriteConfig, r)
	if e != nil {
		return ControlReply{}, e
	}
	defer rollback(tx)
	current, e := readConfig(ctx, tx, true)
	if e != nil {
		return ControlReply{}, e
	}
	// Recheck time after waiting for the serialized config lock. Auth rows remain locked.
	if tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&state.now) != nil {
		return ControlReply{}, adminauth.ErrAuthUnavailable
	}
	if e = adminauth.CheckSession(state.account, state.session, state.policy, state.now); e != nil {
		return ControlReply{}, e
	}
	key := sha256.Sum256([]byte(r.Header.Get("Idempotency-Key")))
	fingerprint := sha256.Sum256(append([]byte("control.replace-draft.v1\x00"+r.Header.Get("If-Match")+"\x00"), raw...))
	var oldHash, body []byte
	var created, expires time.Time
	var result ControlReply
	e = tx.QueryRow(ctx, "SELECT request_hash,created_at,expires_at,status,response,etag FROM control_commands WHERE actor_id=$1 AND key_hash=$2", state.account.ID, key[:]).Scan(&oldHash, &created, &expires, &result.Status, &body, &result.ETag)
	if e == nil {
		if state.now.Before(created) {
			return ControlReply{}, adminauth.ErrAuthUnavailable
		}
		if state.now.Before(expires) {
			if string(oldHash) != string(fingerprint[:]) {
				return replyProblem(409, "IDEMPOTENCY_CONFLICT"), nil
			}
			if tx.Commit(ctx) != nil {
				return ControlReply{}, adminauth.ErrAuthUnavailable
			}
			result.Body = body
			result.Replay = true
			return result, nil
		}
		if _, e = tx.Exec(ctx, "DELETE FROM control_commands WHERE actor_id=$1 AND key_hash=$2", state.account.ID, key[:]); e != nil {
			return ControlReply{}, adminauth.ErrAuthUnavailable
		}
	} else if !errors.Is(e, pgx.ErrNoRows) {
		return ControlReply{}, adminauth.ErrAuthUnavailable
	}
	// Bounded housekeeping; the cap remains conservative if there is a backlog.
	if _, e = tx.Exec(ctx, "DELETE FROM control_commands WHERE (actor_id,key_hash) IN (SELECT actor_id,key_hash FROM control_commands WHERE expires_at<=$1 ORDER BY expires_at LIMIT 128)", state.now); e != nil {
		return ControlReply{}, adminauth.ErrAuthUnavailable
	}
	var count int
	if tx.QueryRow(ctx, "SELECT count(*) FROM control_commands").Scan(&count) != nil || count >= maxControlCommands {
		return replyProblem(503, "COMMAND_CAPACITY_EXCEEDED"), nil
	}
	next := control.Config{}
	result = ControlReply{Status: 200, ETag: configETag(current.Revision)}
	switch {
	case r.Header.Get("If-Match") != configETag(current.Revision):
		result = replyProblem(412, "REVISION_MISMATCH")
	case control.DecodeExact(raw, &next) != nil:
		result = replyProblem(400, "INVALID_REQUEST")
	case next.Revision != current.Revision || next.Profile != current.Profile || next.RegionID != current.RegionID:
		result = replyProblem(422, "UNSUPPORTED_CAPABILITY")
	default:
		if e = next.Validate(); errors.Is(e, control.ErrConflict) {
			result = replyProblem(409, "ROUTE_CONFLICT")
		} else if e != nil {
			result = replyProblem(422, "INVALID_CONFIG")
		}
	}
	before := digest(current.Bytes())
	after := before
	revision := current.Revision
	outcome := "rejected"
	if result.Status == 200 {
		next.Revision++
		result.Body = next.Bytes()
		result.ETag = configETag(next.Revision)
		after = digest(result.Body)
		revision = next.Revision
		outcome = "saved_draft"
		if len(result.Body) > control.MaxConfigBytes {
			return replyProblem(422, "INVALID_CONFIG"), nil
		}
		if _, e = tx.Exec(ctx, "UPDATE control_config SET revision=$1,document=$2,updated_at=$3 WHERE singleton", revision, result.Body, state.now); e != nil {
			return ControlReply{}, adminauth.ErrAuthUnavailable
		}
	}
	id, _, e := adminauth.NewCSRFToken()
	if e != nil {
		return ControlReply{}, adminauth.ErrAuthUnavailable
	}
	if _, e = tx.Exec(ctx, "INSERT INTO control_audit(actor_id,actor_role,action,target_id,before_digest,after_digest,result,request_id,revision) VALUES($1,$2,'config.save_draft','installation',$3,$4,$5,$6,$7)", state.account.ID, state.account.Role, before, after, outcome, id, revision); e != nil {
		return ControlReply{}, adminauth.ErrAuthUnavailable
	}
	if _, e = tx.Exec(ctx, "INSERT INTO control_commands(actor_id,key_hash,request_hash,created_at,expires_at,status,response,etag) VALUES($1,$2,$3,$4::timestamptz,$4::timestamptz+interval '24 hours',$5,$6,$7)", state.account.ID, key[:], fingerprint[:], state.now, result.Status, result.Body, result.ETag); e != nil {
		return ControlReply{}, adminauth.ErrAuthUnavailable
	}
	if _, e = tx.Exec(ctx, "UPDATE auth_sessions SET last_seen_at=$1 WHERE token_hash=$2", state.now, mustSessionHash(token)); e != nil {
		return ControlReply{}, adminauth.ErrAuthUnavailable
	}
	if tx.Commit(ctx) != nil {
		return ControlReply{}, adminauth.ErrAuthUnavailable
	}
	return result, nil
}
func mustSessionHash(token string) []byte { h, _ := tokenHash(token); return h[:] }

// Audit uses an exclusive upper-bound ID cursor (0 starts at newest) and a fixed
// 50-row page. Bodies, URLs, credentials and raw idempotency keys are never selected.
func (s *ControlService) Audit(ctx context.Context, token string, before int64) ([]AuditEvent, error) {
	return s.AuditPage(ctx, token, before, 50)
}
func (s *ControlService) AuditPage(ctx context.Context, token string, before int64, limit int) ([]AuditEvent, error) {
	if before < 0 || limit < 1 || limit > 100 {
		return nil, control.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, _, e := s.begin(ctx, token, adminauth.ReadAudit, nil)
	if e != nil {
		return nil, e
	}
	defer rollback(tx)
	query := "SELECT id,occurred_at,actor_id,actor_role,action,target_id,before_digest,after_digest,result,request_id,revision FROM control_audit"
	args := []any{}
	if before > 0 {
		query += " WHERE id<$1"
		args = append(args, before)
	}
	args = append(args, limit)
	query += " ORDER BY id DESC LIMIT $" + strconv.Itoa(len(args))
	rows, e := tx.Query(ctx, query, args...)
	if e != nil {
		return nil, adminauth.ErrAuthUnavailable
	}
	out := []AuditEvent{}
	for rows.Next() {
		var a AuditEvent
		if e = rows.Scan(&a.ID, &a.OccurredAt, &a.ActorID, &a.ActorRole, &a.Action, &a.TargetID, &a.BeforeDigest, &a.AfterDigest, &a.Result, &a.RequestID, &a.Revision); e != nil {
			rows.Close()
			return nil, adminauth.ErrAuthUnavailable
		}
		out = append(out, a)
	}
	rows.Close()
	if rows.Err() != nil || tx.Commit(ctx) != nil {
		return nil, adminauth.ErrAuthUnavailable
	}
	return out, nil
}

func (r ControlReply) String() string {
	return fmt.Sprintf("control response status=%d replay=%t", r.Status, r.Replay)
}
