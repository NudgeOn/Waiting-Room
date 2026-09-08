// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"waiting-room/internal/adminauth"
	"waiting-room/internal/control"
)

func readEvents(ctx context.Context, tx pgx.Tx, room string) ([]control.Event, error) {
	rows, err := tx.Query(ctx, "SELECT id,room_id,prequeue_at,admit_at,drain_at,state FROM control_events WHERE room_id=$1 ORDER BY prequeue_at,id LIMIT 1001", room)
	if err != nil {
		return nil, adminauth.ErrAuthUnavailable
	}
	defer rows.Close()
	result := []control.Event{}
	for rows.Next() {
		var e control.Event
		if rows.Scan(&e.ID, &e.RoomID, &e.PrequeueAt, &e.AdmitAt, &e.DrainAt, &e.State) != nil {
			return nil, adminauth.ErrAuthUnavailable
		}
		result = append(result, e)
	}
	if rows.Err() != nil || len(result) > 1000 {
		return nil, adminauth.ErrAuthUnavailable
	}
	return result, nil
}
func (s *PublicationService) Events(ctx context.Context, token, room string) (ControlReply, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, _, err := s.control.begin(ctx, token, adminauth.ReadEvents, nil)
	if err != nil {
		return ControlReply{}, err
	}
	defer rollback(tx)
	d, _, err := readDelivery(ctx, tx)
	if err != nil {
		return ControlReply{}, err
	}
	_, runtime, ok := d.Find(room)
	if !ok {
		return replyProblem(404, "NOT_FOUND"), nil
	}
	events, err := readEvents(ctx, tx, room)
	if err != nil {
		return ControlReply{}, err
	}
	if tx.Commit(ctx) != nil {
		return ControlReply{}, adminauth.ErrAuthUnavailable
	}
	return jsonReply(200, map[string]any{"items": events}, runtime.ETag()), nil
}
func (s *PublicationService) ChangeEvent(ctx context.Context, token, roomID, eventID string, r *http.Request, raw []byte) (ControlReply, error) {
	target := roomID + "/events"
	if eventID != "" {
		target = "events/" + eventID
	}
	return s.control.command(ctx, token, r, raw, adminauth.WriteEvents, target, true, func(ctx context.Context, tx pgx.Tx, state sessionSnapshot, current control.Config) (commandResult, error) {
		out := commandResult{}
		if (eventID == "" && r.Method != "POST") || (eventID != "" && r.Method != "PUT" && r.Method != "DELETE" && r.Method != "POST") {
			out.reply = replyProblem(405, "INVALID_REQUEST")
			return out, nil
		}
		d, generation, err := readDelivery(ctx, tx)
		if err != nil {
			return out, err
		}
		out.before = digest(d.Bytes())
		if eventID != "" {
			if tx.QueryRow(ctx, "SELECT room_id FROM control_events WHERE id=$1", eventID).Scan(&roomID) != nil {
				out.reply = replyProblem(404, "NOT_FOUND")
				return out, nil
			}
		}
		room, runtime, ok := d.Find(roomID)
		if !ok {
			out.reply = replyProblem(404, "NOT_FOUND")
			return out, nil
		}
		if r.Header.Get("If-Match") != runtime.ETag() {
			out.reply = replyProblem(412, "REVISION_MISMATCH")
			return out, nil
		}
		events, err := readEvents(ctx, tx, roomID)
		if err != nil {
			return out, err
		}
		var next control.Event
		found := false
		for _, event := range events {
			if event.ID == eventID {
				next = event
				found = true
			}
		}
		if eventID != "" && (!found || next.State == "cancelled" || next.State == "completed") {
			out.reply = replyProblem(409, "EVENT_INACTIVE")
			return out, nil
		}
		if r.Method == "DELETE" {
			if len(raw) != 0 && control.DecodeExact(raw, &struct{}{}) != nil {
				out.reply = replyProblem(400, "INVALID_REQUEST")
				return out, nil
			}
			next.State = "cancelled"
			// Cancelling a schedule does not silently change current traffic mode.
		} else {
			if !room.Active {
				out.reply = replyProblem(409, "ROOM_INACTIVE")
				return out, nil
			}
			if eventID != "" && r.Method == "POST" {
				if control.DecodeExact(raw, &struct{}{}) != nil {
					out.reply = replyProblem(400, "INVALID_REQUEST")
					return out, nil
				}
				if next.State != "paused_by_override" || !state.now.Before(next.DrainAt) {
					out.reply = replyProblem(409, "EVENT_INACTIVE")
					return out, nil
				}
				next.State = "scheduled"
			} else {
				var input control.EventInput
				if control.DecodeExact(raw, &input) != nil {
					out.reply = replyProblem(400, "INVALID_REQUEST")
					return out, nil
				}
				if input.Validate(state.now) != nil {
					out.reply = replyProblem(422, "INVALID_EVENT")
					return out, nil
				}
				for _, event := range events {
					if event.ID != eventID && event.Overlaps(input) {
						out.reply = replyProblem(409, "EVENT_OVERLAP")
						return out, nil
					}
				}
				if eventID == "" {
					if len(events) >= 1000 {
						out.reply = replyProblem(422, "EVENT_CAPACITY_EXCEEDED")
						return out, nil
					}
					id, _, err := adminauth.NewCSRFToken()
					if err != nil {
						return out, err
					}
					eventID = id
				}
				next = control.Event{ID: eventID, RoomID: roomID, PrequeueAt: input.PrequeueAt.UTC(), AdmitAt: input.AdmitAt.UTC(), DrainAt: input.DrainAt.UTC(), State: "scheduled"}
			}
		}
		if _, err = tx.Exec(ctx, "INSERT INTO control_events(id,room_id,prequeue_at,admit_at,drain_at,state) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(id) DO UPDATE SET prequeue_at=EXCLUDED.prequeue_at,admit_at=EXCLUDED.admit_at,drain_at=EXCLUDED.drain_at,state=EXCLUDED.state", next.ID, next.RoomID, next.PrequeueAt, next.AdmitAt, next.DrainAt, next.State); err != nil {
			return out, adminauth.ErrAuthUnavailable
		}
		events, err = readEvents(ctx, tx, roomID)
		if err != nil {
			return out, err
		}
		runtime.EventState = "none"
		for _, event := range events {
			if event.State == "paused_by_override" {
				runtime.EventState = "paused_by_override"
				break
			}
			if event.State == "running" {
				runtime.EventState = "running"
				break
			}
			if event.State == "scheduled" {
				runtime.EventState = "scheduled"
			}
		}
		runtime.Revision++
		for i := range d.Runtimes {
			if d.Runtimes[i].RoomID == roomID {
				d.Runtimes[i].Runtime = runtime
			}
		}
		if err = s.sign(ctx, tx, d, generation, state.now); err != nil {
			return out, err
		}
		eventBytes, _ := json.Marshal(next)
		out.after = digest(append(d.Bytes(), eventBytes...))
		out.revision = runtime.Revision
		out.reply = jsonReply(200, next, runtime.ETag())
		return out, nil
	})
}

// Tick serializes with manual commands. It never advances a paused event and
// requires fresh, matching ACKs plus a full arrival window before safe OFF.
func (s *PublicationService) Tick(ctx context.Context) error {
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
	if tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now) != nil {
		return adminauth.ErrAuthUnavailable
	}
	v, err := deliveryView(ctx, tx, d, generation)
	if err != nil {
		return err
	}
	changed := false
	for i, room := range d.Config.Rooms {
		if !room.Active {
			continue
		}
		runtime := d.Runtimes[i].Runtime
		before := digest(d.Bytes())
		events, err := readEvents(ctx, tx, room.ID)
		if err != nil {
			return err
		}
		var active *control.Event
		for j := range events {
			if events[j].State != "cancelled" && events[j].State != "completed" {
				active = &events[j]
				break
			}
		}
		if active != nil {
			mode, state := control.NextEventMode(*active, now)
			if mode != "" && (mode != runtime.Mode || active.State != state || runtime.EventState != state) {
				runtime.Mode = mode
				runtime.EventState = state
				if _, err = tx.Exec(ctx, "UPDATE control_events SET state=$1 WHERE id=$2", state, active.ID); err != nil {
					return adminauth.ErrAuthUnavailable
				}
			}
		}
		if runtime.Mode == "DRAINING" && safeDrain(v, room.ID, runtime) {
			runtime.Mode = "OFF"
			runtime.EventState = "none"
			if active != nil && active.State == "paused_by_override" {
				runtime.EventState = "paused_by_override"
			}
			if active != nil && active.State != "paused_by_override" {
				if _, err = tx.Exec(ctx, "UPDATE control_events SET state='completed' WHERE id=$1", active.ID); err != nil {
					return adminauth.ErrAuthUnavailable
				}
			}
		}
		if runtime != d.Runtimes[i].Runtime {
			runtime.Revision++
			d.Runtimes[i].Runtime = runtime
			changed = true
			id, _, err := adminauth.NewCSRFToken()
			if err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, "INSERT INTO control_audit(actor_id,actor_role,action,target_id,before_digest,after_digest,result,request_id,revision) VALUES('system:scheduler','system','events.transition',$1,$2,$3,'accepted',$4,$5)", room.ID, before, digest(d.Bytes()), id, runtime.Revision); err != nil {
				return adminauth.ErrAuthUnavailable
			}
		}
	}
	if changed {
		if err = s.sign(ctx, tx, d, generation, now); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func safeDrain(view DeliveryView, room string, runtime control.Runtime) bool {
	if view.State != "applied" {
		return false
	}
	queueOK, originOK := false, false
	for _, node := range view.Nodes {
		for _, m := range node.Rooms {
			if m.RoomID != room || m.Mode != "DRAINING" || m.Revision != runtime.Revision || m.Epoch != runtime.Epoch {
				continue
			}
			if node.ID == "coordinator" {
				queueOK = m.Waiting == 0 && m.Ready == 0 && m.RecoveryUntil == 0
			}
			if node.ID == "gateway" {
				originOK = m.OriginHealthy && m.ArrivalWindowReady && m.ArrivalsFiveMinutes <= runtime.Limits.AdmissionsPerMinute*5
			}
		}
	}
	return queueOK && originOK
}
