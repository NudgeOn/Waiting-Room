// SPDX-License-Identifier: Apache-2.0
package installplan

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
)

type ReportInput struct {
	SchemaVersion int        `json:"schemaVersion"`
	Plan          Input      `json:"plan"`
	Cost          *CostInput `json:"cost,omitempty"`
}

type PlanningPayload struct {
	Kind              string      `json:"kind"`
	PlanDigest        string      `json:"planDigest"`
	Plan              Plan        `json:"plan"`
	Cost              *CostReport `json:"cost"`
	CostStatus        string      `json:"costStatus"`
	Installation      string      `json:"installation"`
	ActivationAllowed bool        `json:"activationAllowed"`
	Qualification     string      `json:"qualification"`
	Warning           string      `json:"warning"`
}

// This checksum detects accidental changes only. It is not a signature,
// authorization, probe record, completed install report or import manifest.
type PlanningReport struct {
	SchemaVersion int             `json:"schemaVersion"`
	Payload       PlanningPayload `json:"payload"`
	SHA256        string          `json:"sha256"`
	HashEncoding  string          `json:"hashEncoding"`
}

func BuildReport(in ReportInput) (PlanningReport, error) {
	if in.SchemaVersion != 1 {
		return PlanningReport{}, ErrInput
	}
	plan, err := Build(in.Plan)
	if err != nil {
		return PlanningReport{}, err
	}
	digest, err := Digest(in.Plan)
	if err != nil {
		return PlanningReport{}, err
	}
	p := PlanningPayload{Kind: "offline-planning-report", PlanDigest: digest, Plan: plan, CostStatus: "NOT_REQUESTED", Installation: "NOT_RUN", Qualification: "NOT_RUN", Warning: "User-priced planning only. No installation, environment probe, activation or qualification has occurred. Checksum is not a signature or permission. Normal input values are retained; do not include secrets."}
	if in.Cost != nil {
		if in.Cost.RegionID != in.Plan.RegionID {
			return PlanningReport{}, ErrInput
		}
		cost, err := Estimate(*in.Cost)
		if err != nil {
			return PlanningReport{}, err
		}
		p.Cost = &cost
		p.CostStatus = "REFERENCE_SUBTOTALS_ONLY"
	}
	b, err := json.Marshal(p)
	if err != nil {
		return PlanningReport{}, ErrInput
	}
	sum := sha256.Sum256(b)
	return PlanningReport{SchemaVersion: 1, Payload: p, SHA256: hex.EncodeToString(sum[:]), HashEncoding: "SHA-256 of Go encoding/json compact payload, struct field order as emitted, HTML escaping enabled, UTF-8, no trailing newline; not RFC 8785."}, nil
}

// Only input values are accepted, never client-computed totals or claimed
// readiness. Decode each nested object through its existing strict validator.
func DecodeReport(r io.Reader) (ReportInput, error) {
	_, root, err := decodeObject(r)
	if err != nil {
		return ReportInput{}, err
	}
	names := []string{"schemaVersion", "plan"}
	if _, ok := root["cost"]; ok {
		names = append(names, "cost")
	}
	if !keys(root, names...) {
		return ReportInput{}, ErrInput
	}
	version, ok := root["schemaVersion"].(json.Number)
	if !ok || version.String() != "1" {
		return ReportInput{}, ErrInput
	}
	p, err := json.Marshal(root["plan"])
	if err != nil {
		return ReportInput{}, ErrInput
	}
	plan, err := Decode(bytes.NewReader(p))
	if err != nil {
		return ReportInput{}, err
	}
	in := ReportInput{SchemaVersion: 1, Plan: plan}
	if raw, ok := root["cost"]; ok {
		b, err := json.Marshal(raw)
		if err != nil {
			return ReportInput{}, ErrInput
		}
		cost, err := DecodeCost(bytes.NewReader(b))
		if err != nil {
			return ReportInput{}, err
		}
		in.Cost = &cost
	}
	if _, err := BuildReport(in); err != nil {
		return ReportInput{}, err
	}
	return in, nil
}
