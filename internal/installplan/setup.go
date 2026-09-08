// SPDX-License-Identifier: Apache-2.0
package installplan

import (
	"bytes"
	"encoding/json"
	"io"
	"regexp"
	"waiting-room/internal/adminauth"
)

var setupDigestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

// SetupPlan is executable only within an already initialized, isolated local
// Compose installation. The offline 10K/100K proposal remains non-executable.
type SetupPlan struct {
	SchemaVersion int                   `json:"schemaVersion"`
	Kind          string                `json:"kind"`
	Input         Input                 `json:"input"`
	Calibration   adminauth.Calibration `json:"calibration"`
	AdminURL      string                `json:"adminUrl"`
	GatewayURL    string                `json:"gatewayUrl"`
	Changes       []string              `json:"changes"`
	NotVerified   []string              `json:"notVerified"`
}

func BuildSetup(in Input, c adminauth.Calibration) (SetupPlan, error) {
	if _, err := Build(in); err != nil || in.Profile != "standard-10k" {
		return SetupPlan{}, ErrInput
	}
	if c.Version != 1 || c.MemoryKiB != adminauth.PasswordMemoryKiB || c.Parallelism != 1 || !c.TargetMet || c.Iterations < 2 || c.Iterations > 10 {
		return SetupPlan{}, ErrInput
	}
	return SetupPlan{1, "local-compose-setup", in, c, "https://127.0.0.1:19443", "https://127.0.0.1:20443",
		[]string{"save-installation-region-and-profile", "save-new-room-traffic-defaults", "set-initial-totp-policy", "persist-password-parameters", "save-setup-report-and-audit"},
		[]string{"public-dns-and-tls", "host-ntp-offset", "production-origin-protection", "quick-20", "10k-performance", "high-scale-and-ha"}}, nil
}

type SetupApply struct {
	Input             Input  `json:"input"`
	PlanDigest        string `json:"planDigest"`
	CalibrationDigest string `json:"calibrationDigest"`
}

func DecodeSetupApply(r io.Reader) (SetupApply, error) {
	b, root, err := decodeObject(r)
	if err != nil || !keys(root, "input", "planDigest", "calibrationDigest") {
		return SetupApply{}, ErrInput
	}
	in, _ := json.Marshal(root["input"])
	input, err := Decode(bytes.NewReader(in))
	if err != nil {
		return SetupApply{}, err
	}
	var out SetupApply
	if json.Unmarshal(b, &out) != nil {
		return SetupApply{}, ErrInput
	}
	out.Input = input
	if !setupDigestPattern.MatchString(out.PlanDigest) || !setupDigestPattern.MatchString(out.CalibrationDigest) {
		return SetupApply{}, ErrInput
	}
	return out, nil
}
