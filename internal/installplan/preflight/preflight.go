// SPDX-License-Identifier: Apache-2.0
// Package preflight evaluates collector evidence without running probes or
// granting activation. Evidence authenticity and scheduler persistence belong
// to the future production adapters, not this pure policy layer.
package preflight

import (
	"regexp"
	"time"

	"waiting-room/internal/installplan"
)

type Status string

const (
	Pass          Status = "PASS"
	Warn          Status = "WARN"
	Fail          Status = "FAIL"
	Unverified    Status = "UNVERIFIED"
	NotDue        Status = "NOT_DUE"
	NotApplicable Status = "NOT_APPLICABLE"
	// Evidence is valid for one minute; this is a conservative policy-layer
	// default, not proof of an operationally qualified sampling interval.
	MaxEvidenceAge = time.Minute
)

// Stamp references a sanitized collector artifact, never an endpoint URL,
// command output, credential or raw clock peer address. SHA-256 references
// bind evidence to a plan but do not authenticate it.
type Stamp struct {
	PlanDigest     string    `json:"planDigest"`
	ArtifactDigest string    `json:"artifactDigest"`
	ObservedAt     time.Time `json:"observedAt"`
}

type ClockEvidence struct {
	Stamp        Stamp
	Method       string // chrony, ntp, or platform-time-service
	SourceDigest string // digest of a separately retained, sanitized source record
	Offset       time.Duration
	Synchronized bool
}

type StoreEvidence struct {
	Stamp                                                  Stamp
	TLS, Authentication, Role, ReadWrite, ConnectionSwitch Status
}

type OriginEvidence struct {
	Stamp       Stamp
	ActiveProbe Status // only PASS/FAIL; absent or unsupported stays UNVERIFIED
}

type Replicas struct {
	Desired int
	Ready   int
}

// Snapshot must describe all pods in the selected event's current rollout,
// not a successful subset. A collector is responsible for this assertion.
type EventSnapshot struct {
	Stamp                         Stamp
	EventDigest                   string
	Gateway, Coordinator, Control Replicas
	ValkeyP95RTT                  time.Duration
	RTTSamples                    int
}

type Event struct {
	Digest              string
	AdmitAt             time.Time
	PreScale, Readiness *EventSnapshot
}

type Observations struct {
	Clock              *ClockEvidence
	PostgreSQL, Valkey *StoreEvidence
	Origin             *OriginEvidence
	// Acknowledgement is not HA verification. Actual topology/backup/HA
	// records and qualification remain mandatory production follow-up work.
	HARecordAcknowledged bool
	Event                *Event
}

type Check struct {
	ID                string `json:"id"`
	Status            Status `json:"status"`
	Code              string `json:"code"`
	BlocksActivation  bool   `json:"blocksActivation"`
	Evidence          *Stamp `json:"evidence,omitempty"`
	ClockMethod       string `json:"clockMethod,omitempty"`
	ClockSourceDigest string `json:"clockSourceDigest,omitempty"`
	ClockOffsetNanos  *int64 `json:"clockOffsetNanos,omitempty"`
}

type Report struct {
	SchemaVersion     int       `json:"schemaVersion"`
	Scope             string    `json:"scope"`
	PlanDigest        string    `json:"planDigest"`
	EvaluatedAt       time.Time `json:"evaluatedAt"`
	Checks            []Check   `json:"checks"`
	HasBlockingChecks bool      `json:"hasBlockingChecks"`
	ActivationAllowed bool      `json:"activationAllowed"`
	Qualification     string    `json:"qualification"`
}

var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

// Evaluate never uses wall clock, network or environment state implicitly.
// Passing selected checks is not a production activation decision.
func Evaluate(plan installplan.Input, planDigest string, now time.Time, o Observations) Report {
	r := Report{SchemaVersion: 1, Scope: "offline-preflight-policy-only", PlanDigest: planDigest, EvaluatedAt: now.UTC(), Qualification: "NOT_RUN"}
	expectedDigest, err := installplan.Digest(plan)
	if err != nil || !digestPattern.MatchString(planDigest) || expectedDigest != planDigest || now.IsZero() {
		r.PlanDigest = "" // do not reflect arbitrary caller input
		r.Checks = []Check{check("input", Fail, "INVALID_PREFLIGHT_CONTEXT", nil)}
		r.HasBlockingChecks = true
		return r
	}
	r.Checks = append(r.Checks, clock(planDigest, now, o.Clock))
	high := plan.Profile == "high-scale-100k"
	r.Checks = append(r.Checks, store("postgresql", planDigest, now, o.PostgreSQL, high), store("valkey", planDigest, now, o.Valkey, high))
	if o.Origin == nil || !fresh(o.Origin.Stamp, planDigest, now) {
		r.Checks = append(r.Checks, check("origin-protection", Unverified, "NO_CURRENT_ACTIVE_PROBE", nil))
	} else {
		r.Checks = append(r.Checks, probe("origin-protection", o.Origin.ActiveProbe, o.Origin.Stamp))
	}
	if high {
		code := "HA_OPERATOR_RECORD_MISSING"
		status := Unverified
		if o.HARecordAcknowledged {
			status = Warn
			code = "OPERATOR_ACK_ONLY_NOT_HA_PROOF"
		}
		r.Checks = append(r.Checks, check("ha-record", status, code, nil))
		if o.Event == nil {
			r.Checks = append(r.Checks, check("event-prescale", Unverified, "EVENT_CONTEXT_REQUIRED", nil), check("event-readiness", Unverified, "EVENT_CONTEXT_REQUIRED", nil))
		} else {
			r.Checks = append(r.Checks, eventCheckpoint(planDigest, now, *o.Event, false), eventCheckpoint(planDigest, now, *o.Event, true))
		}
	} else {
		r.Checks = append(r.Checks, check("ha-record", NotApplicable, "STANDARD_IS_NOT_HA", nil), check("event-prescale", NotApplicable, "HIGH_SCALE_CHECK_ONLY", nil), check("event-readiness", NotApplicable, "HIGH_SCALE_CHECK_ONLY", nil))
	}
	for _, c := range r.Checks {
		if c.BlocksActivation {
			r.HasBlockingChecks = true
		}
	}
	return r
}

func check(id string, status Status, code string, s *Stamp) Check {
	c := Check{ID: id, Status: status, Code: code, BlocksActivation: status == Fail || status == Unverified || status == NotDue}
	if s != nil {
		copy := *s
		copy.ObservedAt = copy.ObservedAt.UTC()
		c.Evidence = &copy
	}
	return c
}

func fresh(s Stamp, planDigest string, at time.Time) bool {
	return s.PlanDigest == planDigest && digestPattern.MatchString(s.ArtifactDigest) && !s.ObservedAt.IsZero() && !s.ObservedAt.After(at) && at.Sub(s.ObservedAt) <= MaxEvidenceAge
}

func clock(planDigest string, now time.Time, e *ClockEvidence) Check {
	if e == nil || !fresh(e.Stamp, planDigest, now) || !digestPattern.MatchString(e.SourceDigest) ||
		(e.Method != "chrony" && e.Method != "ntp" && e.Method != "platform-time-service") {
		return check("clock", Unverified, "CLOCK_EVIDENCE_MISSING_OR_INVALID", nil)
	}
	status, code := Pass, "CLOCK_WITHIN_RECOMMENDATION"
	// Compare both signs directly: abs(MinInt64) overflows.
	if !e.Synchronized {
		status, code = Fail, "CLOCK_NOT_SYNCHRONIZED"
	} else if e.Offset > 5*time.Second || e.Offset < -5*time.Second {
		status, code = Fail, "CLOCK_OFFSET_EXCEEDS_5_SECONDS"
	} else if e.Offset > 2*time.Second || e.Offset < -2*time.Second {
		status, code = Warn, "CLOCK_OFFSET_EXCEEDS_2_SECONDS"
	}
	c := check("clock", status, code, &e.Stamp)
	c.ClockMethod = e.Method
	c.ClockSourceDigest = e.SourceDigest
	n := int64(e.Offset)
	c.ClockOffsetNanos = &n
	return c
}

func probe(id string, status Status, s Stamp) Check {
	switch status {
	case Pass:
		return check(id, Pass, "PROBE_PASSED_NOT_HA_PROOF", &s)
	case Fail:
		return check(id, Fail, "PROBE_FAILED", &s)
	default:
		return check(id, Unverified, "PROBE_NOT_VERIFIED", &s)
	}
}

func store(id, planDigest string, now time.Time, e *StoreEvidence, high bool) Check {
	if e == nil || !fresh(e.Stamp, planDigest, now) {
		return check(id, Unverified, "STORE_EVIDENCE_MISSING_OR_INVALID", nil)
	}
	statuses := []Status{e.Authentication, e.Role, e.ReadWrite}
	if high {
		statuses = append(statuses, e.TLS, e.ConnectionSwitch)
	}
	// Known failure wins over missing evidence regardless of field ordering.
	status := Pass
	for _, s := range statuses {
		if s == Fail {
			status = Fail
			break
		}
		if s != Pass {
			status = Unverified
		}
	}
	return probe(id, status, e.Stamp)
}

func eventCheckpoint(planDigest string, now time.Time, event Event, ready bool) Check {
	id, lead, snapshot := "event-prescale", 10*time.Minute, event.PreScale
	if ready {
		id, lead, snapshot = "event-readiness", 5*time.Minute, event.Readiness
	}
	if event.AdmitAt.IsZero() || !digestPattern.MatchString(event.Digest) {
		return check(id, Fail, "INVALID_EVENT_CONTEXT", nil)
	}
	deadline := event.AdmitAt.Add(-lead)
	if now.Before(deadline) {
		return check(id, NotDue, "CHECKPOINT_NOT_DUE_NO_APPROVAL", nil)
	}
	// Evaluate the bounded historical deadline sample, not a later healthy
	// sample that would silently erase a missed checkpoint. The scheduler must
	// persist checkpoint decisions and recheck live readiness before activation.
	if snapshot == nil {
		return check(id, Unverified, "CHECKPOINT_EVIDENCE_MISSING", nil)
	}
	if snapshot.EventDigest != event.Digest || !fresh(snapshot.Stamp, planDigest, deadline) {
		return check(id, Fail, "CHECKPOINT_EVIDENCE_LATE_STALE_OR_MISMATCHED", nil)
	}
	for _, r := range []Replicas{snapshot.Gateway, snapshot.Coordinator, snapshot.Control} {
		if r.Desired < 0 || r.Ready < 0 || r.Ready > r.Desired {
			return check(id, Fail, "INVALID_REPLICA_COUNTS", &snapshot.Stamp)
		}
	}
	if snapshot.Gateway.Desired < 8 || snapshot.Gateway.Desired > 12 || snapshot.Coordinator.Desired < 4 || snapshot.Coordinator.Desired > 8 || snapshot.Control.Desired != 2 || snapshot.Gateway.Ready < 8 || snapshot.Coordinator.Ready < 4 {
		return check(id, Fail, "PRESCALE_TARGET_NOT_AVAILABLE", &snapshot.Stamp)
	}
	if ready {
		if snapshot.Gateway.Ready != snapshot.Gateway.Desired || snapshot.Coordinator.Ready != snapshot.Coordinator.Desired || snapshot.Control.Ready != snapshot.Control.Desired {
			return check(id, Fail, "ROLLOUT_NOT_ALL_READY", &snapshot.Stamp)
		}
		if snapshot.RTTSamples < 1 || snapshot.ValkeyP95RTT <= 0 {
			return check(id, Unverified, "RTT_MEASUREMENT_REQUIRED", &snapshot.Stamp)
		}
		if snapshot.ValkeyP95RTT >= 2*time.Millisecond {
			return check(id, Fail, "VALKEY_P95_RTT_NOT_BELOW_2MS", &snapshot.Stamp)
		}
	}
	return check(id, Pass, "CHECKPOINT_SAMPLE_PASSED_NOT_LIVE_APPROVAL", &snapshot.Stamp)
}
