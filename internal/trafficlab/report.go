// SPDX-License-Identifier: Apache-2.0
// Package trafficlab runs bounded HTTP correctness exercises against a fixed
// sample fixture. It cannot accept an operator URL or a production Room.
package trafficlab

import (
	"context"
	"errors"
	"time"
)

const MaxDuration = 90 * time.Second
const MaxReportBytes = 32768

var ErrRun = errors.New("TRAFFIC_LAB_UNAVAILABLE")

type Input struct {
	RunID  string `json:"runId"`
	Preset string `json:"preset"`
}

func Visitors(preset string) int {
	switch preset {
	case "quick-20":
		return 20
	case "smoke-1k":
		return 1000
	}
	return 0
}

type Check struct {
	Name     string `json:"name"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
	Passed   bool   `json:"passed"`
}
type Event struct {
	Visitor  int       `json:"visitor"`
	Client   string    `json:"client"`
	Sequence uint64    `json:"sequence"`
	Expected string    `json:"expected"`
	Actual   string    `json:"actual"`
	Passed   bool      `json:"passed"`
	At       time.Time `json:"at"`
}
type Report struct {
	SchemaVersion      int        `json:"schemaVersion"`
	ScenarioVersion    int        `json:"scenarioVersion"`
	Preset             string     `json:"preset"`
	Scope              string     `json:"scope"`
	Engine             string     `json:"engine"`
	State              string     `json:"state"`
	Stage              string     `json:"stage"`
	Visitors           int        `json:"visitors"`
	Joined             int        `json:"joined"`
	Admitted           int        `json:"admitted"`
	Queued             int        `json:"queued"`
	Requests           int        `json:"requests"`
	ExpectedRejections int        `json:"expectedRejections"`
	UnexpectedErrors   int        `json:"unexpectedErrors"`
	DurationMS         int64      `json:"durationMs"`
	P95MS              int64      `json:"p95Ms"`
	StartedAt          time.Time  `json:"startedAt"`
	FinishedAt         *time.Time `json:"finishedAt"`
	Checks             []Check    `json:"checks"`
	Timeline           []Event    `json:"timeline"`
}

func NewReport(preset string) Report {
	return Report{SchemaVersion: 1, ScenarioVersion: 1, Preset: preset, Scope: "isolated-sample-origin", Engine: "valkey-runtime-v4", State: "running", Stage: "starting", Visitors: Visitors(preset), StartedAt: time.Now().UTC(), Checks: []Check{}, Timeline: []Event{}}
}

type Execute func(context.Context, Input, func(Report) error) (Report, error)

func (r Report) Valid(preset string) bool {
	if r.SchemaVersion != 1 || r.ScenarioVersion != 1 || r.Preset != preset || Visitors(preset) == 0 || r.Visitors != Visitors(preset) || r.Scope != "isolated-sample-origin" || r.Engine != "valkey-runtime-v4" || len(r.Timeline) > 20 || len(r.Checks) > 16 || r.Joined < 0 || r.Joined > r.Visitors || r.Admitted < 0 || r.Admitted > 3 || r.Queued < 0 || r.Queued > r.Visitors || r.Requests < 0 || r.Requests > 15000 || r.DurationMS < 0 || r.DurationMS > 120000 || r.P95MS < 0 || r.P95MS > 5000 || r.ExpectedRejections < 0 || r.UnexpectedErrors < 0 {
		return false
	}
	if r.State != "running" && r.State != "passed" && r.State != "failed" && r.State != "cancelled" && r.State != "interrupted" {
		return false
	}
	if r.Stage != "starting" && r.Stage != "joining" && r.Stage != "checking" && r.Stage != "finished" {
		return false
	}
	if r.StartedAt.IsZero() || r.ExpectedRejections+r.UnexpectedErrors > r.Requests || r.Admitted+r.Queued > r.Joined {
		return false
	}
	if r.State != "running" && (r.Stage != "finished" || r.FinishedAt == nil || r.FinishedAt.Before(r.StartedAt)) {
		return false
	}
	seen := map[string]bool{}
	for _, c := range r.Checks {
		if seen[c.Name] || len(c.Expected) == 0 || len(c.Expected) > 256 || len(c.Actual) == 0 || len(c.Actual) > 256 {
			return false
		}
		switch c.Name {
		case "join-retry", "fifo", "lease-cap", "claim-retry", "early-claim", "origin-protection", "coordinator-loss", "scenario-stage":
		default:
			return false
		}
		seen[c.Name] = true
	}
	for _, e := range r.Timeline {
		if e.Visitor < 1 || e.Visitor > r.Visitors || e.Sequence < 1 || e.Sequence > uint64(r.Visitors) || (e.Client != "browser" && e.Client != "app") || (e.Expected != "admitted" && e.Expected != "queued") || (e.Actual != "admitted" && e.Actual != "queued") || e.At.IsZero() || e.Passed != (e.Expected == e.Actual) {
			return false
		}
	}
	if r.State == "passed" {
		if len(r.Checks) != 7 || seen["scenario-stage"] || len(r.Timeline) != 20 || r.ExpectedRejections != 3 || r.Joined != r.Visitors || r.Admitted != 3 || r.Queued != r.Visitors-3 || r.UnexpectedErrors != 0 || r.FinishedAt == nil {
			return false
		}
		for _, c := range r.Checks {
			if !c.Passed {
				return false
			}
		}
	}
	return true
}
