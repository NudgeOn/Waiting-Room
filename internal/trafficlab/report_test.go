// SPDX-License-Identifier: Apache-2.0
package trafficlab

import (
	"testing"
	"time"
)

func completeReport() Report {
	r := NewReport("quick-20")
	r.State = "passed"
	r.Stage = "finished"
	r.Joined = 20
	r.Admitted = 3
	r.Queued = 17
	r.Requests = 93
	r.ExpectedRejections = 3
	now := time.Now().UTC()
	r.FinishedAt = &now
	for _, name := range []string{"join-retry", "fifo", "lease-cap", "claim-retry", "early-claim", "origin-protection", "coordinator-loss"} {
		r.Checks = append(r.Checks, Check{name, "expected", "actual", true})
	}
	for i := 1; i <= 20; i++ {
		state := "queued"
		if i <= 3 {
			state = "admitted"
		}
		r.Timeline = append(r.Timeline, Event{i, "app", uint64(i), state, state, true, now})
	}
	return r
}
func TestReportRejectsIncompleteOrContradictoryPass(t *testing.T) {
	if !completeReport().Valid("quick-20") {
		t.Fatal("complete result rejected")
	}
	for _, mutate := range []func(*Report){
		func(r *Report) { r.Checks[0].Passed = false }, func(r *Report) { r.Checks[0].Name = "fifo" }, func(r *Report) { r.Scope = "production" }, func(r *Report) { r.Engine = "model" },
		func(r *Report) { r.Joined = 19 }, func(r *Report) { r.UnexpectedErrors = 1 }, func(r *Report) { r.FinishedAt = nil }, func(r *Report) { r.Timeline = nil },
		func(r *Report) { r.ExpectedRejections = 94 }, func(r *Report) { r.Timeline[0].Actual = "unknown" },
	} {
		r := completeReport()
		mutate(&r)
		if r.Valid("quick-20") {
			t.Fatal("invalid pass accepted", r)
		}
	}
}
