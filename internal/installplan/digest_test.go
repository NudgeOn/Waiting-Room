// SPDX-License-Identifier: Apache-2.0
package installplan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func TestDigestBindsCanonicalPlan(t *testing.T) {
	in, err := Decode(strings.NewReader(string(fixture(t, "standard"))))
	if err != nil {
		t.Fatal(err)
	}
	d, err := Digest(in)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := Build(in)
	b, _ := json.Marshal(p)
	sum := sha256.Sum256(b)
	if d != hex.EncodeToString(sum[:]) {
		t.Fatal("wrong canonical digest")
	}
	for _, mutate := range []func(*Input){func(i *Input) { i.TOTP.Enabled = false }, func(i *Input) { i.RegionID = "tokyo" }, func(i *Input) { i.Profile = "high-scale-100k" }, func(i *Input) { i.Limits.AdmissionsPerMinute++ }} {
		changed := in
		mutate(&changed)
		next, err := Digest(changed)
		if err != nil || next == d {
			t.Fatal("plan change not bound")
		}
	}
	in.Profile = "unsupported"
	if d, err := Digest(in); err != ErrInput || d != "" {
		t.Fatal("invalid plan hashed")
	}
}
