// SPDX-License-Identifier: Apache-2.0
package installplan

import (
	"encoding/json"
	"strings"
	"testing"
	"waiting-room/internal/adminauth"
)

func TestSetupInputStrictAndLocalOnly(t *testing.T) {
	in := Input{SchemaVersion: 1, Profile: "standard-10k", RegionID: "local", QueuePolicy: "fifo", ExpectedPeakVisitors: 100, Limits: Limits{100, 60, 900}, TOTP: TOTP{"configurable", false}}
	c := adminauth.Calibration{Version: 1, MemoryKiB: 65536, Parallelism: 1, Iterations: 3, TargetMet: true}
	if _, err := BuildSetup(in, c); err != nil {
		t.Fatal(err)
	}
	apply := SetupApply{in, strings.Repeat("a", 64), strings.Repeat("b", 64)}
	raw, _ := json.Marshal(apply)
	if _, err := DecodeSetupApply(strings.NewReader(string(raw))); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{strings.Replace(string(raw), `"schemaVersion":1`, `"schemaVersion":1,"schemaVersion":1`, 1), strings.Replace(string(raw), `"enabled":false`, `"enabled":null`, 1), strings.Replace(string(raw), `"enabled":false`, `"enabled":false,"secret":"fixture"`, 1), string(raw) + `{}`, strings.Replace(string(raw), `"planDigest":`, `"PlanDigest":`, 1)} {
		if _, err := DecodeSetupApply(strings.NewReader(bad)); err == nil {
			t.Fatal("ambiguous apply accepted")
		}
	}
	in.Profile = "high-scale-100k"
	if _, err := BuildSetup(in, c); err == nil {
		t.Fatal("unsupported Helm apply accepted")
	}
	in.Profile = "standard-10k"
	c.TargetMet = false
	if _, err := BuildSetup(in, c); err == nil {
		t.Fatal("failed calibration accepted")
	}
}
