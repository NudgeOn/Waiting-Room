// SPDX-License-Identifier: Apache-2.0
package installplan

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("../../test/installplan/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestProfiles(t *testing.T) {
	for _, name := range []string{"standard", "high-scale"} {
		t.Run(name, func(t *testing.T) {
			in, err := Decode(strings.NewReader(string(fixture(t, name))))
			if err != nil {
				t.Fatal(err)
			}
			p, err := Build(in)
			if err != nil {
				t.Fatal(err)
			}
			cap := 10000
			if name == "high-scale" {
				cap = 100000
			}
			if p.VisitorStateCap != cap || p.IdempotencyBudget != cap*2 || p.RateCapPerMinute != cap*6/10 {
				t.Fatal("profile caps")
			}
			if p.Executable || p.ActivationAllowed || p.Qualification != "NOT_RUN" || !strings.HasPrefix(p.CostStatus, "NOT_CALCULATED") {
				t.Fatal("false completion claim")
			}
			if len(p.Resources) != 5 || len(p.CostExcluded) != 6 {
				t.Fatal("missing reference/exclusions")
			}
			if name == "high-scale" && (p.Resources[0].Replicas != 4 || p.Resources[3].Ownership != "operator-external" || p.Deployment != "helm-application-only") {
				t.Fatal("high scale responsibility")
			}
			if name == "standard" && (p.Resources[3].Ownership != "bundled-store" || p.Deployment != "compose-single-host") {
				t.Fatal("standard responsibility")
			}
		})
	}
}

func TestValidationBoundaries(t *testing.T) {
	for _, name := range []string{"standard", "high-scale"} {
		base, err := Decode(strings.NewReader(string(fixture(t, name))))
		if err != nil {
			t.Fatal(err)
		}
		cap, rate := 10000, 6000
		if name == "high-scale" {
			cap, rate = 100000, 60000
		}
		cases := []struct {
			name   string
			modify func(*Input)
		}{
			{"peak-zero", func(i *Input) { i.ExpectedPeakVisitors = 0 }},
			{"peak-over", func(i *Input) { i.ExpectedPeakVisitors = cap + 1 }},
			{"lease-zero", func(i *Input) { i.Limits.MaxActiveAdmissionLeases = 0 }},
			{"lease-over", func(i *Input) { i.Limits.MaxActiveAdmissionLeases = cap + 1 }},
			{"rate-zero", func(i *Input) { i.Limits.AdmissionsPerMinute = 0 }},
			{"rate-over", func(i *Input) { i.Limits.AdmissionsPerMinute = rate + 1 }},
			{"ttl-low", func(i *Input) { i.Limits.AdmissionTTLSeconds = 59 }},
			{"ttl-high", func(i *Input) { i.Limits.AdmissionTTLSeconds = 3601 }},
			{"forced-off", func(i *Input) { i.TOTP = TOTP{"forced_on", false} }},
			{"unknown-policy", func(i *Input) { i.QueuePolicy = "lottery" }},
			{"multi-region", func(i *Input) { i.RegionID = "seoul,tokyo" }},
			{"unknown-profile", func(i *Input) { i.Profile = "other" }},
			{"version", func(i *Input) { i.SchemaVersion = 2 }},
			{"totp-mode", func(i *Input) { i.TOTP.Mode = "other" }},
			{"region-empty", func(i *Input) { i.RegionID = "" }},
			{"region-long", func(i *Input) { i.RegionID = strings.Repeat("a", 64) }},
		}
		for _, tc := range cases {
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				in := base
				tc.modify(&in)
				if _, err := Build(in); err != ErrInput {
					t.Fatal("must reject without values", err)
				}
			})
		}
		for _, ttl := range []int{60, 3600} {
			for _, enabled := range []bool{true, false} {
				in := base
				in.ExpectedPeakVisitors = cap
				in.Limits = Limits{cap, rate, ttl}
				in.TOTP = TOTP{"configurable", enabled}
				if _, err := Build(in); err != nil {
					t.Fatal("valid boundary", err)
				}
			}
		}
	}
}

func TestStrictDecodeAndNoErrorEcho(t *testing.T) {
	b := string(fixture(t, "standard"))
	for name, input := range map[string]string{
		"unknown-secret":    strings.Replace(b, `"schemaVersion": 1`, `"password": "DO-NOT-ECHO", "schemaVersion": 1`, 1),
		"nested-secret":     strings.Replace(b, `"mode": "configurable"`, `"password": "DO-NOT-ECHO", "mode": "configurable"`, 1),
		"duplicate":         strings.Replace(b, `"enabled": true`, `"enabled": false, "enabled": true`, 1),
		"duplicate-escaped": strings.Replace(b, `"enabled": true`, `"enabled": false, "\u0065nabled": true`, 1),
		"wrong-case":        strings.Replace(b, `"profile"`, `"Profile"`, 1),
		"missing-bool":      strings.Replace(b, `, "enabled": true`, "", 1),
		"null":              strings.Replace(b, `"enabled": true`, `"enabled": null`, 1),
		"wrong-type":        strings.Replace(b, `"enabled": true`, `"enabled": "DO-NOT-ECHO"`, 1),
		"multiple":          b + b,
		"array":             `["DO-NOT-ECHO"]`,
		"malformed":         `{"DO-NOT-ECHO`,
		"oversize":          b + strings.Repeat(" ", MaxInputBytes),
		"regions":           strings.Replace(b, `"regionId": "ap-northeast-2"`, `"regionId": ["seoul","tokyo"]`, 1),
		"nested-case":       strings.Replace(b, `"enabled"`, `"Enabled"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Decode(strings.NewReader(input))
			if err != ErrInput || strings.Contains(err.Error(), "DO-NOT-ECHO") {
				t.Fatal("unsafe decode result")
			}
		})
	}
}

func TestDeterministicPlanAndOwnedSlices(t *testing.T) {
	b := fixture(t, "standard")
	var object map[string]any
	if err := json.Unmarshal(b, &object); err != nil {
		t.Fatal(err)
	}
	reordered, _ := json.Marshal(object)
	in, _ := Decode(strings.NewReader(string(b)))
	want, _ := Build(in)
	expected, _ := json.Marshal(want)
	for n := 0; n < 100; n++ {
		t.Run("repeat", func(t *testing.T) {
			t.Parallel()
			in, err := Decode(strings.NewReader(string(reordered)))
			if err != nil {
				t.Fatal(err)
			}
			p, _ := Build(in)
			actual, _ := json.Marshal(p)
			if string(actual) != string(expected) {
				t.Fatal("not byte stable")
			}
			p.RequiredChecks[0] = "mutated"
			p.Resources[0].Name = "mutated"
		})
	}
}

func FuzzDecode(f *testing.F) {
	b, _ := os.ReadFile("../../test/installplan/standard.json")
	f.Add(string(b))
	f.Add("{}")
	f.Add("null")
	f.Fuzz(func(t *testing.T, s string) {
		in, err := Decode(strings.NewReader(s))
		if err != nil {
			if err != ErrInput {
				t.Fatal("error leaked")
			}
			return
		}
		p, err := Build(in)
		if err != nil || p.Executable || p.ActivationAllowed {
			t.Fatal("unsafe plan")
		}
	})
}
