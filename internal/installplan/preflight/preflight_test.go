// SPDX-License-Identifier: Apache-2.0
package preflight

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"waiting-room/internal/installplan"
)

func fixture(high bool) (installplan.Input, string, time.Time, Observations) {
	p := installplan.Input{SchemaVersion: 1, Profile: "standard-10k", RegionID: "seoul", QueuePolicy: "fifo", ExpectedPeakVisitors: 10000, Limits: installplan.Limits{MaxActiveAdmissionLeases: 1000, AdmissionsPerMinute: 600, AdmissionTTLSeconds: 900}, TOTP: installplan.TOTP{Mode: "configurable", Enabled: true}}
	if high {
		p.Profile = "high-scale-100k"
	}
	d, _ := installplan.Digest(p)
	now := time.Date(2026, 9, 5, 13, 0, 0, 0, time.UTC)
	s := Stamp{PlanDigest: d, ArtifactDigest: strings.Repeat("b", 64), ObservedAt: now}
	o := Observations{Clock: &ClockEvidence{Stamp: s, Method: "chrony", SourceDigest: strings.Repeat("c", 64), Synchronized: true}, PostgreSQL: &StoreEvidence{Stamp: s, TLS: Pass, Authentication: Pass, Role: Pass, ReadWrite: Pass, ConnectionSwitch: Pass}, Valkey: &StoreEvidence{Stamp: s, TLS: Pass, Authentication: Pass, Role: Pass, ReadWrite: Pass, ConnectionSwitch: Pass}, Origin: &OriginEvidence{Stamp: s, ActiveProbe: Pass}, HARecordAcknowledged: true}
	event := Event{Digest: strings.Repeat("d", 64), AdmitAt: now.Add(5 * time.Minute)}
	snap := EventSnapshot{Stamp: s, EventDigest: event.Digest, Gateway: Replicas{8, 8}, Coordinator: Replicas{4, 4}, Control: Replicas{2, 2}, ValkeyP95RTT: time.Millisecond, RTTSamples: 100}
	event.Readiness = &snap
	pre := snap
	pre.Stamp.ObservedAt = event.AdmitAt.Add(-10 * time.Minute)
	event.PreScale = &pre
	o.Event = &event
	return p, d, now, o
}

func find(t *testing.T, r Report, id string) Check {
	t.Helper()
	for _, c := range r.Checks {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("missing %s", id)
	return Check{}
}

func TestClockBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name   string
		offset time.Duration
		want   Status
	}{
		{"zero", 0, Pass}, {"plus2", 2 * time.Second, Pass}, {"minus2", -2 * time.Second, Pass},
		{"above2", 2*time.Second + 1, Warn}, {"belowMinus2", -2*time.Second - 1, Warn},
		{"plus5", 5 * time.Second, Warn}, {"minus5", -5 * time.Second, Warn},
		{"above5", 5*time.Second + 1, Fail}, {"belowMinus5", -5*time.Second - 1, Fail},
		{"minDuration", time.Duration(math.MinInt64), Fail}, {"maxDuration", time.Duration(math.MaxInt64), Fail},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, d, now, o := fixture(false)
			o.Clock.Offset = tc.offset
			c := find(t, Evaluate(p, d, now, o), "clock")
			if c.Status != tc.want || c.ClockOffsetNanos == nil || *c.ClockOffsetNanos != int64(tc.offset) || c.Evidence == nil || c.ClockMethod != "chrony" {
				t.Fatal(c)
			}
		})
	}
}

func TestObservationValidity(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Observations)
		want   Status
	}{
		{"missing", func(o *Observations) { o.Clock = nil }, Unverified},
		{"unsynchronized", func(o *Observations) { o.Clock.Synchronized = false }, Fail},
		{"future", func(o *Observations) { o.Clock.Stamp.ObservedAt = o.Clock.Stamp.ObservedAt.Add(1) }, Unverified},
		{"stale", func(o *Observations) { o.Clock.Stamp.ObservedAt = o.Clock.Stamp.ObservedAt.Add(-time.Minute - 1) }, Unverified},
		{"exact-age", func(o *Observations) { o.Clock.Stamp.ObservedAt = o.Clock.Stamp.ObservedAt.Add(-time.Minute) }, Pass},
		{"wrong-plan", func(o *Observations) { o.Clock.Stamp.PlanDigest = strings.Repeat("a", 64) }, Unverified},
		{"no-artifact", func(o *Observations) { o.Clock.Stamp.ArtifactDigest = "" }, Unverified},
		{"no-source", func(o *Observations) { o.Clock.SourceDigest = "" }, Unverified},
		{"unsafe-method", func(o *Observations) { o.Clock.Method = "DO-NOT-ECHO" }, Unverified},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, d, now, o := fixture(false)
			tc.change(&o)
			r := Evaluate(p, d, now, o)
			c := find(t, r, "clock")
			if c.Status != tc.want {
				t.Fatal(c)
			}
			b, _ := json.Marshal(r)
			if strings.Contains(string(b), "DO-NOT-ECHO") {
				t.Fatal("input echoed")
			}
		})
	}
}

func TestStoreSeverityAndResponsibility(t *testing.T) {
	for _, high := range []bool{false, true} {
		p, d, now, o := fixture(high)
		for _, id := range []string{"postgresql", "valkey"} {
			var e *StoreEvidence
			if id == "postgresql" {
				e = o.PostgreSQL
			} else {
				e = o.Valkey
			}
			e.TLS = ""
			e.ConnectionSwitch = ""
			want := Pass
			if high {
				want = Unverified
			}
			if c := find(t, Evaluate(p, d, now, o), id); c.Status != want {
				t.Fatal(c)
			}
			e.Authentication = Fail
			if c := find(t, Evaluate(p, d, now, o), id); c.Status != Fail {
				t.Fatal("failure must win", c)
			}
			e.Authentication = Pass
			e.Role = Status("DO-NOT-ECHO")
			r := Evaluate(p, d, now, o)
			if c := find(t, r, id); c.Status != Unverified {
				t.Fatal(c)
			}
			b, _ := json.Marshal(r)
			if strings.Contains(string(b), "DO-NOT-ECHO") {
				t.Fatal("status echoed")
			}
		}
	}
	p, d, now, o := fixture(true)
	r := Evaluate(p, d, now, o)
	if c := find(t, r, "ha-record"); c.Status != Warn || c.Code != "OPERATOR_ACK_ONLY_NOT_HA_PROOF" {
		t.Fatal(c)
	}
	o.HARecordAcknowledged = false
	if c := find(t, Evaluate(p, d, now, o), "ha-record"); c.Status != Unverified || !c.BlocksActivation {
		t.Fatal(c)
	}
}

func TestOriginMustHaveActiveProbe(t *testing.T) {
	for _, s := range []Status{"", Warn, NotApplicable, Status("DO-NOT-ECHO"), Pass, Fail} {
		p, d, now, o := fixture(false)
		o.Origin.ActiveProbe = s
		want := s
		if s != Pass && s != Fail {
			want = Unverified
		}
		if c := find(t, Evaluate(p, d, now, o), "origin-protection"); c.Status != want {
			t.Fatal(c)
		}
	}
}

func TestEventDeadlineBoundaries(t *testing.T) {
	for _, ready := range []bool{false, true} {
		p, d, now, o := fixture(true)
		id, lead := "event-prescale", 10*time.Minute
		if ready {
			id, lead = "event-readiness", 5*time.Minute
		}
		deadline := o.Event.AdmitAt.Add(-lead)
		if c := find(t, Evaluate(p, d, deadline.Add(-1), o), id); c.Status != NotDue || !c.BlocksActivation {
			t.Fatal(c)
		}
		if c := find(t, Evaluate(p, d, deadline, o), id); c.Status != Pass {
			t.Fatal(c)
		}
		for _, tc := range []struct {
			offset time.Duration
			want   Status
		}{{0, Pass}, {-time.Minute, Pass}, {-time.Minute - 1, Fail}, {1, Fail}} {
			s := o.Event.PreScale
			if ready {
				s = o.Event.Readiness
			}
			s.Stamp.ObservedAt = deadline.Add(tc.offset)
			if c := find(t, Evaluate(p, d, now.Add(time.Hour), o), id); c.Status != tc.want {
				t.Fatal(tc, c)
			}
		}
	}
}

func TestEventReadinessFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*EventSnapshot)
		want   Status
	}{
		{"lessGateway", func(s *EventSnapshot) { s.Gateway = Replicas{7, 7} }, Fail},
		{"lessCoordinator", func(s *EventSnapshot) { s.Coordinator = Replicas{3, 3} }, Fail},
		{"pendingGateway", func(s *EventSnapshot) { s.Gateway = Replicas{9, 8} }, Fail},
		{"pendingCoordinator", func(s *EventSnapshot) { s.Coordinator = Replicas{5, 4} }, Fail},
		{"pendingControl", func(s *EventSnapshot) { s.Control.Ready = 1 }, Fail},
		{"invalidCounts", func(s *EventSnapshot) { s.Gateway.Ready = 9 }, Fail},
		{"overMax", func(s *EventSnapshot) { s.Gateway = Replicas{13, 13} }, Fail},
		{"negative", func(s *EventSnapshot) { s.Control.Ready = -1 }, Fail},
		{"rttExact2", func(s *EventSnapshot) { s.ValkeyP95RTT = 2 * time.Millisecond }, Fail},
		{"rttBelow2", func(s *EventSnapshot) { s.ValkeyP95RTT = 2*time.Millisecond - 1 }, Pass},
		{"rttZero", func(s *EventSnapshot) { s.ValkeyP95RTT = 0 }, Unverified},
		{"rttNegative", func(s *EventSnapshot) { s.ValkeyP95RTT = -1 }, Unverified},
		{"noSamples", func(s *EventSnapshot) { s.RTTSamples = 0 }, Unverified},
		{"wrongEvent", func(s *EventSnapshot) { s.EventDigest = strings.Repeat("a", 64) }, Fail},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, d, now, o := fixture(true)
			tc.change(o.Event.Readiness)
			if c := find(t, Evaluate(p, d, now, o), "event-readiness"); c.Status != tc.want {
				t.Fatal(c)
			}
		})
	}
}

func TestReportNeverGrantsActivation(t *testing.T) {
	for _, high := range []bool{false, true} {
		p, d, now, o := fixture(high)
		r := Evaluate(p, d, now, o)
		if r.ActivationAllowed || r.Qualification != "NOT_RUN" || r.HasBlockingChecks {
			t.Fatal(r)
		}
		if high {
			o.Event = nil
			if c := find(t, Evaluate(p, d, now, o), "event-readiness"); c.Status != Unverified {
				t.Fatal(c)
			}
		}
		missing := Evaluate(p, d, now, Observations{})
		if !missing.HasBlockingChecks || missing.ActivationAllowed {
			t.Fatal(missing)
		}
		for _, c := range missing.Checks {
			if c.Status != NotApplicable && !c.BlocksActivation {
				t.Fatal(c)
			}
		}
	}
}

func TestMissingAndInvalidEventEvidence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Observations)
		want   Status
	}{
		{"missing-snapshots", func(o *Observations) { o.Event.PreScale = nil; o.Event.Readiness = nil }, Unverified},
		{"invalid-time", func(o *Observations) { o.Event.AdmitAt = time.Time{} }, Fail},
		{"invalid-identity", func(o *Observations) { o.Event.Digest = "" }, Fail},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, d, now, o := fixture(true)
			tc.change(&o)
			r := Evaluate(p, d, now, o)
			for _, id := range []string{"event-prescale", "event-readiness"} {
				c := find(t, r, id)
				if c.Status != tc.want || !c.BlocksActivation {
					t.Fatal(c)
				}
			}
		})
	}
	p, d, now, o := fixture(false)
	if r := Evaluate(p, d, time.Time{}, o); !r.HasBlockingChecks || len(r.Checks) != 1 {
		t.Fatal("zero evaluator clock accepted")
	}
	p.Limits.AdmissionsPerMinute = 6001
	if r := Evaluate(p, d, now, o); !r.HasBlockingChecks || len(r.Checks) != 1 {
		t.Fatal("invalid profile accepted")
	}
}

func TestContextBindingAndDeterminism(t *testing.T) {
	p, d, now, o := fixture(true)
	want := Evaluate(p, d, now, o)
	b, _ := json.Marshal(want)
	for n := 0; n < 50; n++ {
		t.Run("repeat", func(t *testing.T) {
			t.Parallel()
			r := Evaluate(p, d, now.In(time.FixedZone("KST", 9*3600)), o)
			got, _ := json.Marshal(r)
			if string(got) != string(b) {
				t.Fatal("non deterministic")
			}
			r.Checks[0].Evidence.PlanDigest = "mutated"
		})
	}
	for _, bad := range []string{"DO-NOT-ECHO", strings.Repeat("a", 64)} {
		r := Evaluate(p, bad, now, o)
		if !r.HasBlockingChecks || r.PlanDigest != "" || len(r.Checks) != 1 {
			t.Fatal(r)
		}
	}
	changed := p
	changed.TOTP.Enabled = false
	if r := Evaluate(changed, d, now, o); !r.HasBlockingChecks || len(r.Checks) != 1 {
		t.Fatal("changed plan accepted")
	}
}

func FuzzClockFailClosed(f *testing.F) {
	f.Add(int64(0))
	f.Add(int64(math.MinInt64))
	f.Add(int64(5*time.Second + 1))
	f.Fuzz(func(t *testing.T, n int64) {
		p, d, now, o := fixture(false)
		o.Clock.Offset = time.Duration(n)
		r := Evaluate(p, d, now, o)
		c := find(t, r, "clock")
		if (n > int64(5*time.Second) || n < -int64(5*time.Second)) && (c.Status != Fail || !c.BlocksActivation) {
			t.Fatal(c)
		}
		if r.ActivationAllowed {
			t.Fatal("activation granted")
		}
	})
}
