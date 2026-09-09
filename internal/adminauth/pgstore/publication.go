// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"context"
	"crypto/ed25519"
	_ "embed"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"waiting-room/internal/adminauth"
	"waiting-room/internal/configtrust"
	"waiting-room/internal/control"
)

//go:embed migrations/006_publication.sql
var Migration006 string

type PublicationService struct {
	kid           string
	keyGeneration int64
	keyDigest     string
	control       *ControlService
	key           ed25519.PrivateKey
	installation  string
}

func (PublicationService) String() string   { return "[REDACTED_PUBLICATION_SERVICE]" }
func (PublicationService) GoString() string { return "[REDACTED_PUBLICATION_SERVICE]" }
func (PublicationService) MarshalJSON() ([]byte, error) {
	return json.Marshal("[REDACTED_PUBLICATION_SERVICE]")
}

func NewPublicationService(store *Store, origin, installation string, key ed25519.PrivateKey) (*PublicationService, error) {
	c, err := NewControlService(store, origin)
	if err != nil || len(key) != ed25519.PrivateKeySize || !control.IDPattern.MatchString(installation) {
		return nil, adminauth.ErrAuthUnavailable
	}
	return &PublicationService{control: c, key: append(ed25519.PrivateKey(nil), key...), installation: installation, kid: "config-v1"}, nil
}
func readDelivery(ctx context.Context, tx pgx.Tx) (control.Delivery, int64, error) {
	var raw []byte
	var generation int64
	var d control.Delivery
	if err := tx.QueryRow(ctx, "SELECT document,generation FROM control_delivery WHERE singleton FOR UPDATE").Scan(&raw, &generation); err != nil {
		return d, 0, adminauth.ErrAuthUnavailable
	}
	if control.DecodeExact(raw, &d) != nil || d.Validate() != nil {
		return d, 0, adminauth.ErrAuthUnavailable
	}
	return d, generation, nil
}

// sign writes one complete envelope atomically with the requested operation.
// Ed25519 is bounded in-memory computation, not a remote/KMS call in the tx.
func (s *PublicationService) sign(ctx context.Context, tx pgx.Tx, d control.Delivery, generation int64, now time.Time) error {
	if d.Validate() != nil {
		return control.ErrInvalid
	}
	raw, err := configtrust.Sign(s.key, configtrust.Snapshot{SchemaVersion: 1, Installation: s.installation, Generation: uint64(generation + 1), Revision: uint64(d.Config.Revision), IssuedAt: now.Unix(), ExpiresAt: now.Add(24 * time.Hour).Unix(), Kid: s.kid, Payload: d.Bytes()})
	if err != nil {
		return control.ErrInvalid
	}
	_, err = tx.Exec(ctx, "UPDATE control_delivery SET document=$1,generation=$2,envelope=$3,issued_at=$4,expires_at=$5 WHERE singleton", d.Bytes(), generation+1, raw, now, now.Add(24*time.Hour))
	if err != nil {
		return adminauth.ErrAuthUnavailable
	}
	return nil
}
func jsonReply(status int, value any, etag string) ControlReply {
	raw, _ := json.Marshal(value)
	return ControlReply{Status: status, Body: raw, ETag: etag}
}

// Publish accepts only the already-saved draft revision. Route deactivation and
// public-ID/origin changes cannot silently bypass an active queue.
func (s *PublicationService) Publish(ctx context.Context, token string, r *http.Request, raw []byte) (ControlReply, error) {
	return s.control.command(ctx, token, r, raw, adminauth.WriteConfig, "config.publish", true, func(ctx context.Context, tx pgx.Tx, state sessionSnapshot, current control.Config) (commandResult, error) {
		out := commandResult{}
		if r.Method != "POST" || string(raw) != "{}" {
			out.reply = replyProblem(400, "INVALID_REQUEST")
			return out, nil
		}
		if r.Header.Get("If-Match") != configETag(current.Revision) {
			out.reply = replyProblem(412, "REVISION_MISMATCH")
			return out, nil
		}
		previous, generation, err := readDelivery(ctx, tx)
		if err != nil {
			return out, err
		}
		out.before = digest(previous.Bytes())
		next := control.Delivery{Config: current, Runtimes: []control.RoomRuntime{}}
		for _, old := range previous.Config.Rooms {
			found := false
			for _, room := range current.Rooms {
				if room.ID == old.ID {
					found = true
					if room.PublicID != old.PublicID {
						out.reply = replyProblem(409, "ROUTE_CONFLICT")
						return out, nil
					}
				}
			}
			// Never orphan retained tickets, leases or events through config editing.
			if !found {
				out.reply = replyProblem(409, "ROOM_REMOVAL_REQUIRES_ARCHIVE")
				return out, nil
			}
		}
		for _, room := range current.Rooms {
			runtime := control.InitialRuntime(room)
			if len(previous.Runtimes) > 0 {
				runtime.Epoch = previous.Runtimes[0].Runtime.Epoch
				runtime.RecoveryUntil = previous.Runtimes[0].Runtime.RecoveryUntil
			}
			if old, prior, ok := previous.Find(room.ID); ok {
				if prior.Mode != "OFF" && (!room.Active || old.Origin != room.Origin || old.Hostname != room.Hostname || old.QueuePolicy != room.QueuePolicy || !slices.Equal(old.ProtectPrefixes, room.ProtectPrefixes) || !slices.Equal(old.ExcludePrefixes, room.ExcludePrefixes)) {
					out.reply = replyProblem(409, "ACTIVE_ROOM_CHANGE_REQUIRES_DRAIN")
					return out, nil
				}
				runtime = prior
				runtime.Revision++
				runtime.Limits = room.Limits
				if !old.Active && room.Active {
					runtime.Mode = "HOLD"
				}
				if !room.Active {
					runtime.Mode = "OFF"
				}
			}
			next.Runtimes = append(next.Runtimes, control.RoomRuntime{RoomID: room.ID, Runtime: runtime})
		}
		if err = s.sign(ctx, tx, next, generation, state.now); err != nil {
			if errors.Is(err, control.ErrInvalid) {
				out.reply = replyProblem(422, "INVALID_CONFIG")
				return out, nil
			}
			return out, err
		}
		out.after = digest(next.Bytes())
		out.revision = current.Revision
		out.reply = jsonReply(202, map[string]any{"generation": generation + 1, "revision": current.Revision, "state": "pending"}, configETag(current.Revision))
		return out, nil
	})
}

func (s *PublicationService) Runtime(ctx context.Context, token, id string) (ControlReply, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, _, err := s.control.begin(ctx, token, adminauth.ReadRuntime, nil)
	if err != nil {
		return ControlReply{}, err
	}
	defer rollback(tx)
	d, _, err := readDelivery(ctx, tx)
	if err != nil {
		return ControlReply{}, err
	}
	_, runtime, ok := d.Find(id)
	if !ok {
		return replyProblem(404, "NOT_FOUND"), nil
	}
	if tx.Commit(ctx) != nil {
		return ControlReply{}, adminauth.ErrAuthUnavailable
	}
	return jsonReply(200, runtime, runtime.ETag()), nil
}
func (s *PublicationService) Operate(ctx context.Context, token, id string, r *http.Request, raw []byte) (ControlReply, error) {
	var input control.RuntimeCommand
	if r == nil || r.Method != "PATCH" || control.DecodeExact(raw, &input) != nil {
		return replyProblem(400, "INVALID_REQUEST"), nil
	}
	action := adminauth.OperateRuntime
	if input.Action == "instant-off" {
		action = adminauth.InstantOff
	}
	if input.Action == "new-epoch" {
		action = adminauth.NewEpoch
	}
	return s.control.command(ctx, token, r, raw, action, id, true, func(ctx context.Context, tx pgx.Tx, state sessionSnapshot, current control.Config) (commandResult, error) {
		out := commandResult{}
		var command control.RuntimeCommand
		if r.Method != "PATCH" || control.DecodeExact(raw, &command) != nil {
			out.reply = replyProblem(400, "INVALID_REQUEST")
			return out, nil
		}
		d, generation, err := readDelivery(ctx, tx)
		if err != nil {
			return out, err
		}
		out.before = digest(d.Bytes())
		room, runtime, ok := d.Find(id)
		if !ok {
			out.reply = replyProblem(404, "NOT_FOUND")
			return out, nil
		}
		if r.Header.Get("If-Match") != runtime.ETag() {
			out.reply = replyProblem(412, "REVISION_MISMATCH")
			return out, nil
		}
		if command.Action == "new-epoch" {
			if command.Scope != "installation" || command.Limits != nil || command.Generation == nil || *command.Generation < 1 {
				out.reply = replyProblem(400, "INVALID_REQUEST")
				return out, nil
			}
			if *command.Generation != generation {
				out.reply = replyProblem(412, "REVISION_MISMATCH")
				return out, nil
			}
			epoch := runtime.Epoch
			for _, item := range d.Runtimes {
				if item.Runtime.Epoch != epoch {
					return out, adminauth.ErrAuthUnavailable
				}
			}
			if epoch >= 9007199254740989 {
				out.reply = replyProblem(409, "REVISION_MISMATCH")
				return out, nil
			}
			until := state.now.Add(3630 * time.Second).UnixMilli()
			if _, err = tx.Exec(ctx, "UPDATE control_events SET state='paused_by_override' WHERE state IN ('scheduled','running')"); err != nil {
				return out, adminauth.ErrAuthUnavailable
			}
			for i := range d.Runtimes {
				next := &d.Runtimes[i].Runtime
				next.Epoch++
				next.Revision++
				next.RecoveryUntil = until
				if d.Config.Rooms[i].Active {
					next.Mode = "HOLD"
				} else {
					next.Mode = "OFF"
				}
				if next.EventState == "scheduled" || next.EventState == "running" {
					next.EventState = "paused_by_override"
				}
			}
			if err = s.sign(ctx, tx, d, generation, state.now); err != nil {
				return out, err
			}
			_, runtime, _ = d.Find(id)
			out.after = digest(d.Bytes())
			out.revision = runtime.Revision
			out.reply = jsonReply(200, runtime, runtime.ETag())
			return out, nil
		}
		if command.Scope != "" || command.Generation != nil {
			out.reply = replyProblem(400, "INVALID_REQUEST")
			return out, nil
		}

		if !room.Active {
			out.reply = replyProblem(409, "ROOM_INACTIVE")
			return out, nil
		}
		if command.Action != "set-limits" && command.Limits != nil {
			out.reply = replyProblem(400, "INVALID_REQUEST")
			return out, nil
		}
		switch command.Action {
		case "instant-off":
			runtime.Mode = "OFF"
		case "auto":
			runtime.Mode = "AUTO"
		case "hold":
			runtime.Mode = "HOLD"
		case "safe-drain":
			runtime.Mode = "DRAINING"
		case "set-limits":
			if command.Limits == nil || command.Limits.Validate(current.Profile) != nil {
				out.reply = replyProblem(422, "INVALID_CONFIG")
				return out, nil
			}
			runtime.Limits = *command.Limits
		default:
			out.reply = replyProblem(403, "FORBIDDEN")
			return out, nil
		}
		res, err := tx.Exec(ctx, "UPDATE control_events SET state='paused_by_override' WHERE room_id=$1 AND state IN ('scheduled','running')", id)
		if err != nil {
			return out, adminauth.ErrAuthUnavailable
		}
		if res.RowsAffected() > 0 {
			runtime.EventState = "paused_by_override"
		}
		runtime.Revision++
		for i := range d.Runtimes {
			if d.Runtimes[i].RoomID == id {
				d.Runtimes[i].Runtime = runtime
			}
		}
		if err = s.sign(ctx, tx, d, generation, state.now); err != nil {
			return out, err
		}
		out.after = digest(d.Bytes())
		out.revision = runtime.Revision
		out.reply = jsonReply(200, runtime, runtime.ETag())
		return out, nil
	})
}

// Refresh renews the last approved state, never a draft, and never rolls time back.
func (s *PublicationService) Refresh(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := authTx(ctx, s.control.store)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if _, err = readConfig(ctx, tx, true); err != nil {
		return err
	}
	d, generation, err := readDelivery(ctx, tx)
	if err != nil {
		return err
	}
	var now time.Time
	var issued *time.Time
	if tx.QueryRow(ctx, "SELECT clock_timestamp(),issued_at FROM control_delivery WHERE singleton").Scan(&now, &issued) != nil {
		return adminauth.ErrAuthUnavailable
	}
	if issued != nil {
		if now.Before(*issued) {
			return adminauth.ErrAuthUnavailable
		}
		var previous []byte
		if tx.QueryRow(ctx, "SELECT envelope FROM control_delivery WHERE singleton").Scan(&previous) != nil {
			return adminauth.ErrAuthUnavailable
		}
		var envelope struct {
			Snapshot struct {
				Kid string `json:"kid"`
			} `json:"snapshot"`
		}
		if json.Unmarshal(previous, &envelope) != nil {
			return adminauth.ErrAuthUnavailable
		}
		if now.Sub(*issued) < 5*time.Minute && envelope.Snapshot.Kid == s.kid {
			return tx.Commit(ctx)
		}
	}
	if err = s.sign(ctx, tx, d, generation, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *PublicationService) Envelope(ctx context.Context) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var raw []byte
	if s.control.store.pool.QueryRow(ctx, "SELECT envelope FROM control_delivery WHERE singleton AND expires_at>clock_timestamp() AND issued_at<=clock_timestamp()").Scan(&raw) != nil || len(raw) == 0 {
		return nil, adminauth.ErrAuthUnavailable
	}
	return raw, nil
}
