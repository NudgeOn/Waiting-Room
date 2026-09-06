//go:build integration

// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"context"
	"encoding/json"
	"testing"
	"time"
	"waiting-room/internal/control"
)

func TestEventsOverlapOverrideResumeAndCancel(t *testing.T) {
	f, s, g := publicationFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	input := control.EventInput{PrequeueAt: now.Add(time.Hour), AdmitAt: now.Add(2 * time.Hour), DrainAt: now.Add(3 * time.Hour)}
	raw, _ := json.Marshal(input)
	r := draftRequest(g, "event-create-one", `"runtime-1"`)
	r.Method = "POST"
	first, err := s.ChangeEvent(ctx, g.SessionToken(), "sale", "", r, raw)
	if err != nil || first.Status != 200 {
		t.Fatal("create", first.Status, err)
	}
	var e control.Event
	if json.Unmarshal(first.Body, &e) != nil || e.State != "scheduled" {
		t.Fatal("event result")
	}
	replay, err := s.ChangeEvent(ctx, g.SessionToken(), "sale", "", r, raw)
	if err != nil || !replay.Replay || string(first.Body) != string(replay.Body) {
		t.Fatal("replay", err)
	}
	r = draftRequest(g, "event-overlap-one", `"runtime-2"`)
	r.Method = "POST"
	out, err := s.ChangeEvent(ctx, g.SessionToken(), "sale", "", r, raw)
	if err != nil || out.Status != 409 {
		t.Fatal("overlap", out.Status, err)
	}
	r = draftRequest(g, "event-manual-hold", `"runtime-2"`)
	r.Method = "PATCH"
	out, err = s.Operate(ctx, g.SessionToken(), "sale", r, []byte(`{"action":"hold"}`))
	if err != nil || out.Status != 200 {
		t.Fatal(err)
	}
	// A due, paused event must not undo the human's HOLD. Use test-owned DB clock fixtures.
	execSQL(t, f.pool, "UPDATE control_events SET prequeue_at=clock_timestamp()-interval '2 hours',admit_at=clock_timestamp()-interval '1 hour' WHERE id=$1", e.ID)
	if err = s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	current, err := s.Runtime(ctx, g.SessionToken(), "sale")
	var runtime control.Runtime
	_ = json.Unmarshal(current.Body, &runtime)
	if err != nil || runtime.Mode != "HOLD" || runtime.EventState != "paused_by_override" || runtime.Revision != 3 {
		t.Fatal("scheduler undid override", err)
	}
	r = draftRequest(g, "event-resume-one", current.ETag)
	r.Method = "POST"
	out, err = s.ChangeEvent(ctx, g.SessionToken(), "", e.ID, r, []byte("{}"))
	if err != nil || out.Status != 200 {
		t.Fatal("resume", err)
	}
	if err = s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	current, _ = s.Runtime(ctx, g.SessionToken(), "sale")
	_ = json.Unmarshal(current.Body, &runtime)
	if runtime.Mode != "AUTO" || runtime.EventState != "running" {
		t.Fatal("catch-up stage")
	}
	r = draftRequest(g, "event-cancel-one", current.ETag)
	r.Method = "DELETE"
	out, err = s.ChangeEvent(ctx, g.SessionToken(), "", e.ID, r, nil)
	if err != nil || out.Status != 200 {
		t.Fatal("cancel", err)
	}
	if err = s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	current, _ = s.Runtime(ctx, g.SessionToken(), "sale")
	_ = json.Unmarshal(current.Body, &runtime)
	if runtime.Mode != "AUTO" || runtime.EventState != "none" {
		t.Fatal("cancel changed traffic mode")
	}
}

func TestSafeDrainRequiresBothFreshCompleteObservations(t *testing.T) {
	runtime := control.InitialRuntime(draftRoom())
	runtime.Mode = "DRAINING"
	queue := RoomMetrics{RoomID: "sale", Revision: 1, Epoch: 1, Mode: "DRAINING"}
	origin := queue
	origin.OriginHealthy = true
	origin.ArrivalWindowReady = true
	good := DeliveryView{State: "applied", Nodes: []NodeView{{ID: "coordinator", Fresh: true, Rooms: []RoomMetrics{queue}}, {ID: "gateway", Fresh: true, Rooms: []RoomMetrics{origin}}}}
	if !safeDrain(good, "sale", runtime) {
		t.Fatal("valid drain blocked")
	}
	for _, mutate := range []func(*DeliveryView){
		func(v *DeliveryView) { v.State = "pending" },
		func(v *DeliveryView) { v.Nodes[0].Rooms[0].Ready = 1 },
		func(v *DeliveryView) { v.Nodes[0].Rooms[0].Waiting = 1 },
		func(v *DeliveryView) { v.Nodes[0].Rooms[0].RecoveryUntil = 100 },
		func(v *DeliveryView) { v.Nodes[1].Rooms[0].OriginHealthy = false },
		func(v *DeliveryView) { v.Nodes[1].Rooms[0].ArrivalWindowReady = false },
		func(v *DeliveryView) {
			v.Nodes[1].Rooms[0].ArrivalsFiveMinutes = runtime.Limits.AdmissionsPerMinute*5 + 1
		},
		func(v *DeliveryView) { v.Nodes[1].Rooms[0].Revision = 2 },
	} {
		var v DeliveryView
		b, _ := json.Marshal(good)
		_ = json.Unmarshal(b, &v)
		mutate(&v)
		if safeDrain(v, "sale", runtime) {
			t.Fatal("unsafe drain accepted")
		}
	}
}
