// SPDX-License-Identifier: Apache-2.0
package installplan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestReportProvenanceAndChecksum(t *testing.T) {
	in, err := DecodeReport(strings.NewReader(string(fixture(t, "report-example"))))
	if err != nil {
		t.Fatal(err)
	}
	r, err := BuildReport(in)
	if err != nil {
		t.Fatal(err)
	}
	d, _ := Digest(in.Plan)
	if r.Payload.PlanDigest != d || r.Payload.Cost.Input != *in.Cost || r.Payload.Plan.Input != in.Plan {
		t.Fatal("missing inputs")
	}
	if r.Payload.ActivationAllowed || r.Payload.Installation != "NOT_RUN" || r.Payload.Qualification != "NOT_RUN" || r.Payload.CostStatus != "REFERENCE_SUBTOTALS_ONLY" {
		t.Fatal("unsafe claim")
	}
	b, _ := json.Marshal(r.Payload)
	h := sha256.Sum256(b)
	if r.SHA256 != hex.EncodeToString(h[:]) {
		t.Fatal("invalid checksum")
	}
	in.Cost = nil
	without, err := BuildReport(in)
	if err != nil || without.Payload.Cost != nil || without.Payload.CostStatus != "NOT_REQUESTED" || without.SHA256 == r.SHA256 {
		t.Fatal("missing cost misrepresented")
	}
	in.Plan.TOTP.Enabled = false
	changed, _ := BuildReport(in)
	if changed.SHA256 == without.SHA256 || changed.Payload.PlanDigest == without.Payload.PlanDigest {
		t.Fatal("unbound plan change")
	}
}

func TestReportRejectsUntrustedClaims(t *testing.T) {
	b := string(fixture(t, "report-example"))
	for name, raw := range map[string]string{
		"cost-null":          `{"schemaVersion":1,"plan":{},"cost":null}`,
		"extra-claim":        strings.Replace(b, `"schemaVersion":1,`, `"activationAllowed":true,"schemaVersion":1,`, 1),
		"duplicate":          strings.Replace(b, `"enabled":true`, `"enabled":false,"enabled":true`, 1),
		"secret":             strings.Replace(b, `"enabled":true`, `"secret":"DO-NOT-ECHO","enabled":true`, 1),
		"wrong-version":      strings.Replace(b, `"schemaVersion":1,`, `"schemaVersion":2,`, 1),
		"wrong-type-version": strings.Replace(b, `"schemaVersion":1,`, `"schemaVersion":"1",`, 1),
		"missing-plan":       `{"schemaVersion":1}`,
		"invalid-cost":       strings.Replace(b, `"0.10"`, `"-1"`, 1),
		"client-total":       strings.Replace(b, `"currency":"USD"`, `"total":"0","currency":"USD"`, 1),
		"trailing":           b + b,
		"oversized":          b + strings.Repeat(" ", MaxInputBytes),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeReport(strings.NewReader(raw)); err != ErrInput {
				t.Fatal("untrusted input accepted")
			}
		})
	}
	in, _ := DecodeReport(strings.NewReader(b))
	in.Cost.RegionID = "different-region"
	if _, err := BuildReport(in); err != ErrInput {
		t.Fatal("different region accepted")
	}
	in.Cost = nil
	in.Plan.Limits.AdmissionsPerMinute = 6001
	if _, err := BuildReport(in); err != ErrInput {
		t.Fatal("invalid plan accepted")
	}
}

func TestReportDeterminismAndOwnedCopies(t *testing.T) {
	in, _ := DecodeReport(strings.NewReader(string(fixture(t, "report-example"))))
	want, _ := BuildReport(in)
	b, _ := json.Marshal(want)
	for n := 0; n < 50; n++ {
		t.Run("repeat", func(t *testing.T) {
			t.Parallel()
			r, err := BuildReport(in)
			if err != nil {
				t.Fatal(err)
			}
			got, _ := json.Marshal(r)
			if string(got) != string(b) {
				t.Fatal("not deterministic")
			}
			r.Payload.Cost.Profiles[0].Excluded[0] = "mutated"
			r.Payload.Plan.RequiredChecks[0] = "mutated"
		})
	}
}

func FuzzReportDecode(f *testing.F) {
	b, _ := os.ReadFile("../../test/installplan/report-example.json")
	f.Add(string(b))
	f.Add(`{"schemaVersion":1,"plan":{}}`)
	f.Add(`null`)
	f.Fuzz(func(t *testing.T, s string) {
		in, err := DecodeReport(strings.NewReader(s))
		if err != nil {
			if err != ErrInput {
				t.Fatal("non-generic error")
			}
			return
		}
		r, err := BuildReport(in)
		if err != nil || r.Payload.ActivationAllowed || r.Payload.Installation != "NOT_RUN" {
			t.Fatal("unsafe report")
		}
	})
}
