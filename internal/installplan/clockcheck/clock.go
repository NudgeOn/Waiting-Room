// SPDX-License-Identifier: Apache-2.0
// Package clockcheck collects a read-only local chrony diagnostic. It never
// changes the clock, installs software or authorizes application activation.
package clockcheck

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"runtime"
	"time"

	"waiting-room/internal/installplan"
	"waiting-room/internal/installplan/preflight"
)

const Timeout = 3 * time.Second
const MaxOutputBytes = 16384
const MaxReferenceAge = 5 * time.Minute

// Artifact deliberately excludes raw command output, peer addresses and IDs.
// It records daemon-reported state, not authenticated UTC accuracy.
type Artifact struct {
	SchemaVersion int       `json:"schemaVersion"`
	Method        string    `json:"method"`
	Source        string    `json:"source"`
	PlanDigest    string    `json:"planDigest"`
	ObservedAt    time.Time `json:"observedAt"`
	ReferenceAt   time.Time `json:"referenceAt"`
	Stratum       int       `json:"stratum"`
	OffsetNanos   int64     `json:"systemOffsetNanos"`
	RootDelay     int64     `json:"rootDelayNanos"`
	Dispersion    int64     `json:"rootDispersionNanos"`
	Synchronized  bool      `json:"synchronized"`
}

type Report struct {
	SchemaVersion int              `json:"schemaVersion"`
	Scope         string           `json:"scope"`
	CollectorCode string           `json:"collectorCode"`
	Artifact      *Artifact        `json:"artifact,omitempty"`
	Policy        preflight.Report `json:"policy"`
}

type runner func(context.Context) ([]byte, error)

// Collect uses one fixed executable/command and a loopback-only destination.
// Invalid plans are rejected before any subprocess is started.
func Collect(ctx context.Context, plan installplan.Input) (Report, error) {
	return collect(ctx, plan, runtime.GOOS, time.Now, runChrony)
}

func collect(ctx context.Context, plan installplan.Input, platform string, now func() time.Time, run runner) (Report, error) {
	digest, err := installplan.Digest(plan)
	if err != nil {
		return Report{}, installplan.ErrInput
	}
	r := Report{SchemaVersion: 1, Scope: "local-clock-diagnostic-only", CollectorCode: "CLOCK_PLATFORM_UNSUPPORTED"}
	start := now()
	at := start
	var evidence *preflight.ClockEvidence
	if platform == "linux" {
		ctx, cancel := context.WithTimeout(ctx, Timeout)
		defer cancel()
		output, runErr := run(ctx)
		at = now()
		switch {
		case ctx.Err() != nil:
			r.CollectorCode = "CLOCK_COMMAND_CANCELED_OR_TIMED_OUT"
		case runErr != nil:
			r.CollectorCode = "CLOCK_COMMAND_UNAVAILABLE_OR_FAILED"
		case at.Before(start) || at.Sub(start) > Timeout || absDuration(at.UTC().Sub(start.UTC())-at.Sub(start)) > time.Second:
			r.CollectorCode = "CLOCK_COLLECTION_TIME_INVALID"
		default:
			a, code := parseTracking(output, at.UTC())
			r.CollectorCode = code
			if a != nil {
				a.PlanDigest = digest
				a.ObservedAt = at.UTC()
				r.Artifact = a
				if code == "CLOCK_OBSERVED" {
					hash := artifactDigest(*a)
					evidence = &preflight.ClockEvidence{Stamp: preflight.Stamp{PlanDigest: digest, ArtifactDigest: hash, ObservedAt: a.ObservedAt}, Method: "chrony", SourceDigest: hash, Offset: time.Duration(a.OffsetNanos), Synchronized: a.Synchronized}
				}
			}
		}
	}
	r.Policy = preflight.Evaluate(plan, digest, at, preflight.Observations{Clock: evidence})
	return r, nil
}

func artifactDigest(a Artifact) string {
	b, _ := json.Marshal(a)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// DiagnosticOK refers only to the clock check, never the complete installation.
func (r Report) DiagnosticOK() bool {
	for _, c := range r.Policy.Checks {
		if c.ID == "clock" {
			return c.Status == preflight.Pass || c.Status == preflight.Warn
		}
	}
	return false
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
