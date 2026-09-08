//go:build integration

// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
	"waiting-room/internal/adminauth"
	"waiting-room/internal/control"
)

// Audit outage before commit, response loss after commit, and changed request
// bytes must preserve one authoritative mutation for every command variant.
func verifyCommandRetry(t *testing.T, f fixture, invoke func([]byte) (ControlReply, error), raw []byte, action string) {
	t.Helper()
	ctx := context.Background()
	var before, after int
	if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM control_audit WHERE action=$1", action).Scan(&before); err != nil {
		t.Fatal(err)
	}
	execSQL(t, f.pool, "ALTER TABLE control_audit ADD CONSTRAINT fixture_audit_outage CHECK(action<>'"+action+"') NOT VALID")
	if _, err := invoke(raw); err != adminauth.ErrAuthUnavailable {
		t.Fatalf("audit outage did not fail closed: %v", err)
	}
	execSQL(t, f.pool, "ALTER TABLE control_audit DROP CONSTRAINT fixture_audit_outage")
	first, err := invoke(raw)
	if err != nil || first.Status < 200 || first.Status >= 300 || first.Replay {
		t.Fatalf("retry after rollback: %d %v", first.Status, err)
	}
	replay, err := invoke(raw)
	if err != nil || !replay.Replay || first.Status != replay.Status || string(first.Body) != string(replay.Body) || first.ETag != replay.ETag {
		t.Fatalf("lost-response retry: %v", err)
	}
	changedRaw := append(append([]byte{}, raw...), ' ')
	if len(raw) == 0 {
		changedRaw = []byte("{}")
	}
	changed, err := invoke(changedRaw)
	wantConflict := 409
	if len(raw) == 0 {
		wantConflict = 400
	} // DELETE forbids payloads before command dispatch.
	if err != nil || changed.Status != wantConflict {
		t.Fatalf("key reuse with changed payload: %d %v", changed.Status, err)
	}
	if err = f.pool.QueryRow(ctx, "SELECT count(*) FROM control_audit WHERE action=$1", action).Scan(&after); err != nil || after != before+1 {
		t.Fatalf("audit duplicated: %d -> %d, %v", before, after, err)
	}
}
func TestCommandRetryAuditMatrix(t *testing.T) {
	for _, action := range []string{"auto", "hold", "safe-drain", "set-limits", "instant-off", "new-epoch"} {
		t.Run("runtime/"+action, func(t *testing.T) {
			f, p, s, g := securityFixture(t, false)
			body := map[string]any{"action": action}
			if action == "set-limits" {
				body["limits"] = control.Limits{MaxActiveAdmissionLeases: 20, AdmissionsPerMinute: 30, AdmissionTTLSeconds: 60}
			}
			if action == "new-epoch" {
				body["scope"] = "installation"
				body["generation"] = 1
			}
			raw, _ := json.Marshal(body)
			r := draftRequest(g, "runtime-retry-matrix", `"runtime-1"`)
			r.Method = http.MethodPatch
			audit := "runtime.operate"
			if action == "new-epoch" {
				audit = "runtime.new_epoch"
				r.Header.Set("X-Reauth-Token", mintMethodProof(t, s, g, adminauth.NewEpoch, "PATCH", "sale", r.Header.Get("If-Match"), raw, ""))
			}
			if action == "instant-off" {
				audit = "runtime.instant_off"
				r.Header.Set("X-Reauth-Token", mintMethodProof(t, s, g, adminauth.InstantOff, "PATCH", "sale", r.Header.Get("If-Match"), raw, ""))
			}
			verifyCommandRetry(t, f, func(b []byte) (ControlReply, error) {
				return p.Operate(context.Background(), g.SessionToken(), "sale", r, b)
			}, raw, audit)
		})
	}
	for _, method := range []string{"POST", "PUT", "resume", "DELETE"} {
		t.Run("events/"+method, func(t *testing.T) {
			f, p, g := publicationFixture(t)
			ctx := context.Background()
			now := time.Now().UTC()
			raw, _ := json.Marshal(control.EventInput{PrequeueAt: now.Add(time.Hour), AdmitAt: now.Add(2 * time.Hour), DrainAt: now.Add(3 * time.Hour)})
			room, id, etag := "sale", "", `"runtime-1"`
			if method != "POST" {
				r := draftRequest(g, "matrix-event-prepare", etag)
				r.Method = "POST"
				out, err := p.ChangeEvent(ctx, g.SessionToken(), room, id, r, raw)
				if err != nil || out.Status != 200 {
					t.Fatal(err)
				}
				var event control.Event
				if json.Unmarshal(out.Body, &event) != nil {
					t.Fatal("event")
				}
				id = event.ID
				room = ""
				etag = out.ETag
			}
			verb := method
			if method == "resume" {
				execSQL(t, f.pool, "UPDATE control_events SET state='paused_by_override' WHERE id=$1", id)
				verb = "POST"
				raw = []byte("{ \n }")
			}
			if method == "DELETE" {
				raw = []byte("{ \n }")
			}
			r := draftRequest(g, "event-retry-matrix", etag)
			r.Method = verb
			verifyCommandRetry(t, f, func(b []byte) (ControlReply, error) { return p.ChangeEvent(ctx, g.SessionToken(), room, id, r, b) }, raw, "events.write")
		})
	}
	for _, method := range []string{"POST", "PATCH", "reset", "DELETE"} {
		t.Run("users/"+method, func(t *testing.T) {
			f, s, g := usersFixture(t)
			raw := []byte(`{"id":"matrix_user","role":"viewer","password":"` + testPassword + `"}`)
			etag := ""
			verb := method
			if method != "POST" {
				execSQL(t, f.pool, "INSERT INTO auth_accounts(id,role) VALUES('matrix_user','operator')")
				etag = `"user-1"`
				raw = []byte(`{"role":"viewer","enabled":true}`)
			}
			reset := method == "reset"
			if reset {
				verb = "POST"
				raw = []byte("{}")
			}
			if method == "DELETE" {
				raw = nil
			}
			action := "users.write"
			if reset {
				action = "security.totp.reset"
			}
			r := userRequest(t, s, g, verb, "matrix_user", etag, "user-retry-matrix", raw, reset)
			verifyCommandRetry(t, f, func(b []byte) (ControlReply, error) {
				if method == "POST" {
					return s.CreateUser(context.Background(), g.SessionToken(), r, b)
				}
				return s.ChangeUser(context.Background(), g.SessionToken(), "matrix_user", r, b, reset)
			}, raw, action)
		})
	}
}
