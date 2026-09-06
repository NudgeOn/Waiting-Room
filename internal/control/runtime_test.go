// SPDX-License-Identifier: Apache-2.0
package control

import (
	"encoding/json"
	"testing"
	"time"
)

func TestRuntimeDelivery(t *testing.T) {
	c := fixture()
	d := Delivery{Config: c, Runtimes: []RoomRuntime{{c.Rooms[0].ID, InitialRuntime(c.Rooms[0])}}}
	if err := ValidateDelivery(d.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := ValidateDelivery(append([]byte(" "), d.Bytes()...)); err == nil {
		t.Fatal("noncanonical payload")
	}
	d.Runtimes[0].Runtime.Revision = 0
	if d.Validate() == nil {
		t.Fatal("zero runtime revision")
	}
	d.Runtimes[0].Runtime = InitialRuntime(c.Rooms[0])
	d.Runtimes[0].RoomID = "other"
	if d.Validate() == nil {
		t.Fatal("room mismatch")
	}
	d.Runtimes[0].RoomID = c.Rooms[0].ID
	d.Config.Rooms[0].Active = false
	d.Runtimes[0].Runtime.Mode = "AUTO"
	if d.Validate() == nil {
		t.Fatal("inactive room runs queue")
	}
}
func TestEventOrderingOverrideAndOverlap(t *testing.T) {
	now := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
	in := EventInput{now.Add(time.Hour), now.Add(2 * time.Hour), now.Add(3 * time.Hour)}
	if in.Validate(now) != nil {
		t.Fatal("valid event")
	}
	e := Event{ID: "event", RoomID: "sale", PrequeueAt: in.PrequeueAt, AdmitAt: in.AdmitAt, DrainAt: in.DrainAt, State: "scheduled"}
	for _, tc := range []struct {
		offset time.Duration
		mode   string
	}{{0, ""}, {time.Hour, "HOLD"}, {2 * time.Hour, "AUTO"}, {4 * time.Hour, "DRAINING"}} {
		m, _ := NextEventMode(e, now.Add(tc.offset))
		if m != tc.mode {
			t.Fatal(tc, m)
		}
	}
	e.State = "paused_by_override"
	if m, _ := NextEventMode(e, now.Add(4*time.Hour)); m != "" {
		t.Fatal("manual override advanced")
	}
	if !e.Overlaps(in) {
		t.Fatal("overlap")
	}
	if e.Overlaps(EventInput{in.DrainAt, in.DrainAt.Add(time.Hour), in.DrainAt.Add(2 * time.Hour)}) {
		t.Fatal("touching events overlap")
	}
	e.State = "cancelled"
	if e.Overlaps(in) {
		t.Fatal("cancelled event blocks")
	}
	bad := in
	bad.AdmitAt = bad.PrequeueAt
	if bad.Validate(now) == nil {
		t.Fatal("equal timestamps")
	}
	bad = in
	bad.PrequeueAt = now.Add(-time.Second)
	if bad.Validate(now) == nil {
		t.Fatal("past prequeue")
	}
	raw, _ := json.Marshal(in)
	var decoded EventInput
	if DecodeExact(raw, &decoded) != nil {
		t.Fatal("time contract decode")
	}
}
