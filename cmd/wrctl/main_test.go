// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"waiting-room/internal/installplan"
	"waiting-room/internal/installplan/clockcheck"
	"waiting-room/internal/installplan/preflight"
)

func TestDoctorClockCLI(t *testing.T) {
	input, err := os.ReadFile("../../test/installplan/standard.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []preflight.Status{preflight.Pass, preflight.Warn, preflight.Fail, preflight.Unverified} {
		t.Run(string(status), func(t *testing.T) {
			var out, stderr bytes.Buffer
			calls := 0
			collect := func(context.Context, installplan.Input) (clockcheck.Report, error) {
				calls++
				return clockcheck.Report{Policy: preflight.Report{Checks: []preflight.Check{{ID: "clock", Status: status}}, Qualification: "NOT_RUN"}}, nil
			}
			want := 3
			if status == preflight.Pass || status == preflight.Warn {
				want = 0
			}
			if code := runWithClock([]string{"doctor-clock"}, bytes.NewReader(input), &out, &stderr, collect); code != want || calls != 1 || stderr.Len() != 0 || !strings.Contains(out.String(), `"activationAllowed": false`) {
				t.Fatal(code, calls, out.String(), stderr.String())
			}
			out.Reset()
			stderr.Reset()
			if code := runWithClock([]string{"doctor-clock"}, strings.NewReader(`{"password":"DO-NOT-ECHO"}`), &out, &stderr, collect); code != 2 || calls != 1 || out.Len() != 0 || strings.Contains(stderr.String(), "DO-NOT-ECHO") {
				t.Fatal("invalid input reached collector")
			}
			stderr.Reset()
			if code := runWithClock([]string{"doctor-clock", "--host=DO-NOT-ECHO"}, bytes.NewReader(input), &out, &stderr, collect); code != 2 || calls != 1 || strings.Contains(stderr.String(), "DO-NOT-ECHO") {
				t.Fatal("argument injection reached collector")
			}
			stderr.Reset()
			if runWithClock([]string{"doctor-clock"}, bytes.NewReader(input), brokenIO{}, &stderr, collect) != 1 {
				t.Fatal("output failure swallowed")
			}
		})
	}
}

func TestPlanCLI(t *testing.T) {
	b, err := os.ReadFile("../../test/installplan/standard.json")
	if err != nil {
		t.Fatal(err)
	}
	var first bytes.Buffer
	for n := 0; n < 2; n++ {
		var out, stderr bytes.Buffer
		if code := run([]string{"plan"}, bytes.NewReader(b), &out, &stderr); code != 0 || stderr.Len() != 0 {
			t.Fatal(code, stderr.String())
		}
		if n == 0 {
			first.Write(out.Bytes())
		} else if first.String() != out.String() {
			t.Fatal("not stable")
		}
		if !strings.Contains(out.String(), `"executable": false`) {
			t.Fatal("missing disclaimer")
		}
	}
}

func TestCLIRejectsWithoutEcho(t *testing.T) {
	for _, args := range [][]string{nil, {"setup"}, {"apply"}, {"plan", "--password=DO-NOT-ECHO"}, {"DO-NOT-ECHO"}} {
		var out, stderr bytes.Buffer
		if run(args, strings.NewReader(""), &out, &stderr) != 2 || out.Len() != 0 || strings.Contains(stderr.String(), "DO-NOT-ECHO") {
			t.Fatal("invalid command leaked")
		}
	}
	var out, stderr bytes.Buffer
	if run([]string{"plan"}, strings.NewReader(`{"password":"DO-NOT-ECHO"}`), &out, &stderr) != 2 || out.Len() != 0 || strings.Contains(stderr.String(), "DO-NOT-ECHO") {
		t.Fatal("invalid input leaked")
	}
}

type brokenIO struct{}

func TestReportCLI(t *testing.T) {
	b, err := os.ReadFile("../../test/installplan/report-example.json")
	if err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if run([]string{"report"}, bytes.NewReader(b), &out, &stderr) != 0 || stderr.Len() != 0 || !strings.Contains(out.String(), `"installation": "NOT_RUN"`) {
		t.Fatal("report failed")
	}
	out.Reset()
	stderr.Reset()
	if run([]string{"report"}, strings.NewReader(`{"password":"DO-NOT-ECHO"}`), &out, &stderr) != 2 || out.Len() != 0 || strings.Contains(stderr.String(), "DO-NOT-ECHO") {
		t.Fatal("invalid input leaked")
	}
}

func TestEstimateCLI(t *testing.T) {
	b, err := os.ReadFile("../../test/installplan/cost-example.json")
	if err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if run([]string{"estimate"}, bytes.NewReader(b), &out, &stderr) != 0 || stderr.Len() != 0 || !strings.Contains(out.String(), `"roundedSubtotal": "76.00"`) {
		t.Fatal(stderr.String(), out.String())
	}
	out.Reset()
	stderr.Reset()
	if run([]string{"estimate"}, strings.NewReader(`{"password":"DO-NOT-ECHO"}`), &out, &stderr) != 2 || out.Len() != 0 || strings.Contains(stderr.String(), "DO-NOT-ECHO") {
		t.Fatal("input leaked")
	}
	stderr.Reset()
	if run([]string{"estimate"}, bytes.NewReader(b), brokenIO{}, &stderr) != 1 || strings.Contains(stderr.String(), "DO-NOT-ECHO") {
		t.Fatal("output error leaked")
	}
}

func (brokenIO) Read([]byte) (int, error)  { return 0, errors.New("DO-NOT-ECHO") }
func (brokenIO) Write([]byte) (int, error) { return 0, errors.New("DO-NOT-ECHO") }

func TestCLIIOErrors(t *testing.T) {
	var out, stderr bytes.Buffer
	if run([]string{"plan"}, brokenIO{}, &out, &stderr) != 2 || strings.Contains(stderr.String(), "DO-NOT-ECHO") {
		t.Fatal("read error leaked")
	}
	b, _ := os.ReadFile("../../test/installplan/standard.json")
	stderr.Reset()
	if run([]string{"plan"}, bytes.NewReader(b), brokenIO{}, &stderr) != 1 || strings.Contains(stderr.String(), "DO-NOT-ECHO") {
		t.Fatal("write error leaked")
	}
}
