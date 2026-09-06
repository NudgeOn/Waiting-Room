// SPDX-License-Identifier: Apache-2.0
package clockcheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"waiting-room/internal/installplan"
	"waiting-room/internal/installplan/preflight"
)

const tracking = `Reference ID    : CB00710F (203.0.113.15)
Stratum         : 3
Ref time (UTC)  : Sat Sep  5 14:00:00 2026
System time     : 0.000006523 seconds slow of NTP time
Last offset     : -0.000006747 seconds
RMS offset      : 0.000035822 seconds
Frequency       : 3.225 ppm slow
Residual freq   : -0.000 ppm
Skew            : 0.129 ppm
Root delay      : 0.013639022 seconds
Root dispersion : 0.001100737 seconds
Update interval : 64.2 seconds
Leap status     : Normal
`

func plan() installplan.Input {
	return installplan.Input{SchemaVersion: 1, Profile: "standard-10k", RegionID: "example-region", QueuePolicy: "fifo", ExpectedPeakVisitors: 100, Limits: installplan.Limits{MaxActiveAdmissionLeases: 10, AdmissionsPerMinute: 60, AdmissionTTLSeconds: 60}, TOTP: installplan.TOTP{Mode: "configurable", Enabled: true}}
}

var observed = time.Date(2026, 9, 5, 14, 0, 20, 0, time.UTC)

func fixtureReport(t *testing.T, raw string) Report {
	t.Helper()
	r, err := collect(context.Background(), plan(), "linux", func() time.Time { return observed }, func(context.Context) ([]byte, error) { return []byte(raw), nil })
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestClockPolicyAndRedaction(t *testing.T) {
	for _, tc := range []struct {
		name, old, value, code string
		status                 preflight.Status
	}{
		{"normal", "", "", "CLOCK_OBSERVED", preflight.Pass},
		{"two-seconds", "0.000006523 seconds slow", "2.000000000 seconds fast", "CLOCK_OBSERVED", preflight.Pass},
		{"warn-positive", "0.000006523 seconds slow", "2.000000001 seconds fast", "CLOCK_OBSERVED", preflight.Warn},
		{"warn-negative", "0.000006523 seconds slow", "2.000000001 seconds slow", "CLOCK_OBSERVED", preflight.Warn},
		{"five-seconds", "0.000006523 seconds slow", "5.000000000 seconds fast", "CLOCK_UNCERTAINTY_UNVERIFIED", preflight.Unverified},
		{"fail-positive", "0.000006523 seconds slow", "5.000000001 seconds fast", "CLOCK_OBSERVED", preflight.Fail},
		{"fail-negative", "0.000006523 seconds slow", "5.000000001 seconds slow", "CLOCK_OBSERVED", preflight.Fail},
		{"unsynced", "Normal", "Not synchronised", "CLOCK_OBSERVED", preflight.Fail},
		{"zero-stratum", "Stratum         : 3", "Stratum         : 0", "CLOCK_OBSERVED", preflight.Fail},
		{"max-stratum", "Stratum         : 3", "Stratum         : 16", "CLOCK_OBSERVED", preflight.Fail},
		{"no-reference", "CB00710F", "00000000", "CLOCK_OBSERVED", preflight.Fail},
		{"local-mode", "CB00710F (203.0.113.15)", "7F7F0101", "CLOCK_LOCAL_REFERENCE_UNVERIFIED", preflight.Unverified},
		{"insert-leap", "Normal", "Insert second", "CLOCK_LEAP_EVENT_UNVERIFIED", preflight.Unverified},
		{"delete-leap", "Normal", "Delete second", "CLOCK_LEAP_EVENT_UNVERIFIED", preflight.Unverified},
		{"stale", "14:00:00", "13:00:00", "CLOCK_REFERENCE_STALE_OR_FUTURE", preflight.Unverified},
		{"future", "14:00:00", "15:00:00", "CLOCK_REFERENCE_STALE_OR_FUTURE", preflight.Unverified},
		{"uncertainty", "0.001100737 seconds", "6.000000000 seconds", "CLOCK_UNCERTAINTY_UNVERIFIED", preflight.Unverified},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := tracking
			if tc.old != "" {
				raw = strings.Replace(raw, tc.old, tc.value, 1)
			}
			r := fixtureReport(t, raw)
			if r.CollectorCode != tc.code || r.Policy.Checks[0].Status != tc.status {
				t.Fatalf("%+v", r)
			}
			if r.DiagnosticOK() != (tc.status == preflight.Pass || tc.status == preflight.Warn) {
				t.Fatal("wrong exit predicate")
			}
			if r.Policy.ActivationAllowed || !r.Policy.HasBlockingChecks || r.Policy.Qualification != "NOT_RUN" {
				t.Fatal("unsafe approval")
			}
			if r.Artifact == nil {
				t.Fatal("missing sanitized record")
			}
			if stamp := r.Policy.Checks[0].Evidence; stamp != nil && (stamp.ArtifactDigest != artifactDigest(*r.Artifact) || stamp.PlanDigest != r.Artifact.PlanDigest) {
				t.Fatal("wrong binding")
			}
			b, _ := json.Marshal(r)
			for _, secret := range []string{"203.0.113.15", "CB00710F", "Reference ID", "Last offset"} {
				if strings.Contains(string(b), secret) {
					t.Fatal("raw source leak")
				}
			}
		})
	}
	// At exactly 5s with zero uncertainty, the original policy still warns.
	r := fixtureReport(t, strings.NewReplacer("0.000006523 seconds slow", "5.000000000 seconds fast", "0.013639022 seconds", "0 seconds", "0.001100737 seconds", "0 seconds").Replace(tracking))
	if r.Policy.Checks[0].Status != preflight.Warn {
		t.Fatal(r)
	}
	if fixtureReport(t, tracking).Artifact.OffsetNanos != -6523 {
		t.Fatal("must use signed System time, not Last offset")
	}
}

func TestTrackingRejectsAmbiguousOutput(t *testing.T) {
	cases := []string{"", "secret", tracking + tracking, tracking + "Unknown : secret\n", strings.Repeat("x", MaxOutputBytes+1), strings.Replace(tracking, "Normal", "normal", 1), strings.Replace(tracking, "0.000006523", "NaN", 1), strings.Replace(tracking, "0.000006523", "Inf", 1), strings.Replace(tracking, "0.000006523", "-0.1", 1), strings.Replace(tracking, "0.000006523", "1e3", 1), strings.Replace(tracking, "0.000006523", "99999999999999999999", 1), strings.Replace(tracking, "0.000006523", "0.0000000001", 1), strings.Replace(tracking, "0.013639022", "-1.0", 1), strings.Replace(tracking, "CB00710F", "not-an-id", 1), strings.Replace(tracking, "Stratum         : 3", "Stratum         : 03", 1), strings.Replace(tracking, "Sat Sep  5", "bad-date", 1), strings.Replace(tracking, "Leap status", "Leap Status", 1), strings.Replace(tracking, "Normal", "Normal\x00secret", 1)}
	for i, raw := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			r := fixtureReport(t, raw)
			if r.Artifact != nil || r.DiagnosticOK() || r.CollectorCode != "CLOCK_OUTPUT_INVALID" || r.Policy.Checks[0].Status != preflight.Unverified {
				t.Fatal(r)
			}
		})
	}
}

func TestCollectorBoundaries(t *testing.T) {
	calls := 0
	run := func(ctx context.Context) ([]byte, error) {
		calls++
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("no deadline")
		}
		return []byte(tracking), nil
	}
	for _, platform := range []string{"darwin", "windows", "unknown"} {
		r, err := collect(context.Background(), plan(), platform, func() time.Time { return observed }, run)
		if err != nil || r.CollectorCode != "CLOCK_PLATFORM_UNSUPPORTED" || r.DiagnosticOK() {
			t.Fatal(r, err)
		}
	}
	invalid := plan()
	invalid.RegionID = "secret"
	invalid.Profile = "invalid"
	if _, err := collect(context.Background(), invalid, "linux", time.Now, run); err == nil || calls != 0 {
		t.Fatal("invalid plan ran a command")
	}
	for _, tc := range []struct {
		name    string
		elapsed time.Duration
	}{{"backward", -time.Second}, {"slow", 4 * time.Second}} {
		t.Run(tc.name, func(t *testing.T) {
			n := 0
			r, _ := collect(context.Background(), plan(), "linux", func() time.Time {
				n++
				if n == 1 {
					return observed
				}
				return observed.Add(tc.elapsed)
			}, run)
			if r.DiagnosticOK() || r.CollectorCode != "CLOCK_COLLECTION_TIME_INVALID" {
				t.Fatal(r)
			}
		})
	}
	r, _ := collect(context.Background(), plan(), "linux", func() time.Time { return observed }, func(context.Context) ([]byte, error) { return []byte(tracking), errors.New("secret stderr") })
	if r.DiagnosticOK() || r.Artifact != nil || r.CollectorCode != "CLOCK_COMMAND_UNAVAILABLE_OR_FAILED" {
		t.Fatal(r)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r, _ = collect(ctx, plan(), "linux", func() time.Time { return observed }, run)
	if r.DiagnosticOK() || r.CollectorCode != "CLOCK_COMMAND_CANCELED_OR_TIMED_OUT" {
		t.Fatal(r)
	}
	// A plan/policy edit changes the artifact binding.
	p := plan()
	p.TOTP.Enabled = false
	r, _ = collect(context.Background(), p, "linux", func() time.Time { return observed }, run)
	if artifactDigest(*r.Artifact) == artifactDigest(*fixtureReport(t, tracking).Artifact) {
		t.Fatal("plan binding not included")
	}
}

func TestCommandHelper(t *testing.T) {
	mode := os.Getenv("WR_CLOCK_TEST_HELPER")
	if mode == "" {
		return
	}
	switch mode {
	case "success":
		fmt.Print(tracking)
	case "stderr":
		fmt.Fprint(os.Stderr, "private-secret")
		os.Exit(2)
	case "overflow":
		fmt.Print(strings.Repeat("x", MaxOutputBytes+1))
	case "timeout":
		time.Sleep(10 * time.Second)
	}
	os.Exit(0)
}

func TestBoundedSubprocess(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"success", "stderr", "overflow", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			timeout := 3 * time.Second
			if mode == "timeout" {
				timeout = 100 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			out, err := runCommand(ctx, binary, []string{"-test.run=^TestCommandHelper$"}, []string{"WR_CLOCK_TEST_HELPER=" + mode, "GORACE=atexit_sleep_ms=0"})
			if mode == "success" {
				if err != nil || string(out) != tracking {
					t.Fatal(err, string(out))
				}
			} else if err == nil || len(out) != 0 {
				t.Fatal("untrusted output returned")
			}
		})
	}
	// io.Copy must not bypass the cap through a promoted bytes.Buffer.ReadFrom.
	w := &limitedOutput{}
	if _, err := io.Copy(w, io.LimitReader(strings.NewReader(strings.Repeat("x", MaxOutputBytes+1)), MaxOutputBytes+1)); err == nil || !w.overflow || w.buffer.Len() > MaxOutputBytes {
		t.Fatal("unbounded writer")
	}
}

func FuzzTracking(f *testing.F) {
	f.Add(tracking)
	f.Add("")
	f.Add("secret\x00")
	f.Fuzz(func(t *testing.T, raw string) {
		a, code := parseTracking([]byte(raw), observed)
		if a != nil {
			if a.Method != "chrony" || a.Stratum < 0 || a.Stratum > 16 || absDuration(time.Duration(a.OffsetNanos)) >= 100000*time.Second {
				t.Fatal("invalid artifact")
			}
			if code == "CLOCK_OBSERVED" && a.Synchronized && absDuration(time.Duration(a.OffsetNanos)) <= 5*time.Second && time.Duration(a.RootDelay)/2+time.Duration(a.Dispersion)+absDuration(time.Duration(a.OffsetNanos)) > 5*time.Second {
				t.Fatal("accepted uncertainty")
			}
		}
	})
}
