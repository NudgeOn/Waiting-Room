// SPDX-License-Identifier: Apache-2.0
// Package installplan builds an offline proposal, never an executable deployment.
package installplan

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
)

const MaxInputBytes = 16384

var ErrInput = errors.New("invalid install-plan input; see the input schema (values are not logged)")

type Limits struct {
	MaxActiveAdmissionLeases int `json:"maxActiveAdmissionLeases"`
	AdmissionsPerMinute      int `json:"admissionsPerMinute"`
	AdmissionTTLSeconds      int `json:"admissionTtlSeconds"`
}

type TOTP struct {
	Mode    string `json:"mode"`
	Enabled bool   `json:"enabled"`
}

// Input contains non-secret planning values only. Credentials are not accepted.
type Input struct {
	SchemaVersion        int    `json:"schemaVersion"`
	Profile              string `json:"profile"`
	RegionID             string `json:"regionId"`
	QueuePolicy          string `json:"queuePolicy"`
	ExpectedPeakVisitors int    `json:"expectedPeakVisitors"`
	Limits               Limits `json:"limits"`
	TOTP                 TOTP   `json:"totp"`
}

type Resource struct {
	Name          string `json:"name"`
	Ownership     string `json:"ownership"`
	Replicas      int    `json:"replicas"`
	CPUMillicores int    `json:"cpuMillicoresPerReplica"`
	MemoryMiB     int    `json:"memoryMiBPerReplica"`
	StorageGiB    int    `json:"storageGiBPerReplica"`
	Meaning       string `json:"meaning"`
}

type Plan struct {
	SchemaVersion     int        `json:"schemaVersion"`
	Kind              string     `json:"kind"`
	Executable        bool       `json:"executable"`
	ActivationAllowed bool       `json:"activationAllowed"`
	Qualification     string     `json:"qualification"`
	Input             Input      `json:"input"`
	Deployment        string     `json:"deployment"`
	VisitorStateCap   int        `json:"visitorStateCap"`
	IdempotencyBudget int        `json:"idempotencyBudget"`
	RateCapPerMinute  int        `json:"rateCapPerMinute"`
	Resources         []Resource `json:"referenceResources"`
	RequiredChecks    []string   `json:"requiredChecksNotRun"`
	OperatorProvides  []string   `json:"operatorProvides"`
	Warnings          []string   `json:"warnings"`
	CostStatus        string     `json:"costStatus"`
	CostExcluded      []string   `json:"costDefaultExcluded"`
}

var regionPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

// Build validates bounds but does not inspect live state, contact dependencies,
// generate secrets, render manifests, or permit Room activation.
func Build(in Input) (Plan, error) {
	cap, rate := 10000, 6000
	if in.Profile == "high-scale-100k" {
		cap, rate = 100000, 60000
	} else if in.Profile != "standard-10k" {
		return Plan{}, ErrInput
	}
	if in.SchemaVersion != 1 || !regionPattern.MatchString(in.RegionID) || in.QueuePolicy != "fifo" ||
		in.ExpectedPeakVisitors < 1 || in.ExpectedPeakVisitors > cap ||
		in.Limits.MaxActiveAdmissionLeases < 1 || in.Limits.MaxActiveAdmissionLeases > cap ||
		in.Limits.AdmissionsPerMinute < 1 || in.Limits.AdmissionsPerMinute > rate ||
		in.Limits.AdmissionTTLSeconds < 60 || in.Limits.AdmissionTTLSeconds > 3600 ||
		(in.TOTP.Mode != "configurable" && in.TOTP.Mode != "forced_on") || (in.TOTP.Mode == "forced_on" && !in.TOTP.Enabled) {
		return Plan{}, ErrInput
	}
	p := Plan{SchemaVersion: 1, Kind: "offline-install-proposal", Input: in, Qualification: "NOT_RUN",
		VisitorStateCap: cap, IdempotencyBudget: cap * 2, RateCapPerMinute: rate,
		RequiredChecks: []string{"environment-capabilities", "dns-tls-ports-storage", "clock-source-and-offset", "secret-mounts", "signed-config", "origin-protection-active-probe", "quick-20", "profile-qualification"},
		Warnings:       []string{"Planning only: no install, apply, secret generation, network probe or activation has occurred.", "Visitor-state cap counts WAITING + READY + ADMITTED until token expiry plus verifier leeway (at most 30 seconds); it is not TCP or online-user capacity.", "Runtime Room and installation aggregate guards remain required; valid planning inputs do not prove live capacity.", "One Home Region only; live-ticket migration is unsupported.", "Reference BOM is not a performance or HA certification."},
		CostStatus:     "NOT_CALCULATED: use wrctl estimate with explicit non-secret user pricing inputs for reference subtotals; no monetary estimate is made by this plan.",
		CostExcluded:   []string{"CDN/load-balancer", "egress", "external-HA-stores", "backup-object-storage", "Prometheus-retention", "operator-labor"},
	}
	if in.Profile == "standard-10k" {
		p.Deployment = "compose-single-host"
		p.OperatorProvides = []string{"Linux host: 4 vCPU / 8 GiB / 50 GiB SSD / 1 Gbps", "Docker engine", "DNS/TLS", "origin ACL/mTLS"}
		p.Resources = []Resource{
			{"gateway", "application", 1, 1000, 1024, 0, "CPU planning share; not reserved cores"},
			{"control-ui", "application", 1, 250, 768, 0, "CPU planning share; not reserved cores"},
			{"coordinator", "application", 1, 500, 512, 0, "CPU planning share; not reserved cores"},
			{"valkey", "bundled-store", 1, 750, 2048, 10, "maxmemory 1 GiB; noeviction; AOF everysec + RDB; not HA"},
			{"postgresql", "bundled-store", 1, 500, 1536, 20, "single node; not HA"},
		}
		p.Warnings = append(p.Warnings, "Host/OS/runtime headroom is additional to component shares. Lab origin, generators and long-term Prometheus are excluded.")
	} else {
		p.Deployment = "helm-application-only"
		p.OperatorProvides = []string{"Kubernetes: three failure domains; three workers each 8 vCPU / 16 GiB", "Ingress/load-balancer and DNS/TLS", "external HA PostgreSQL and Valkey", "origin ACL/mTLS", "workload identity/mTLS and NetworkPolicy"}
		p.Resources = []Resource{
			{"gateway", "application", 4, 1000, 768, 0, "request; limit 2 CPU / 1536 MiB; event pre-scale 8; maximum 12"},
			{"control-ui", "application", 2, 500, 512, 0, "request"},
			{"coordinator", "application", 4, 1000, 1024, 0, "request; limit 2 CPU / 2048 MiB; maximum 8"},
			{"valkey", "operator-external", 3, 4000, 8192, 50, "primary + two replicas; not provisioned by installer"},
			{"postgresql", "operator-external", 2, 2000, 8192, 100, "primary + standby; not provisioned by installer"},
		}
		p.RequiredChecks = append(p.RequiredChecks, "external-store-tls-auth-role-read-write-switch", "operator-HA-backup-RPO-RTO-record", "three-domain-topology-and-sentinel-quorum-2", "hpa-pdb-and-network-policy", "T-minus-10m-gateway-8-coordinator-4", "T-minus-5m-all-ready-and-valkey-p95-RTT-less-than-2ms")
		p.Warnings = append(p.Warnings, "Endpoint connectivity cannot prove replicas, zones, backup, HA or RPO/RTO. External stores and cluster are operator responsibilities.")
	}
	return p, nil
}

// Decode requires an exact, bounded JSON object. Go's default case-insensitive
// field matching and duplicate-key last-write-wins behavior are not accepted.
func Decode(r io.Reader) (Input, error) {
	b, root, err := decodeObject(r)
	if err != nil {
		return Input{}, err
	}
	if !keys(root, "schemaVersion", "profile", "regionId", "queuePolicy", "expectedPeakVisitors", "limits", "totp") {
		return Input{}, ErrInput
	}
	limits, ok := root["limits"].(map[string]any)
	if !ok || !keys(limits, "maxActiveAdmissionLeases", "admissionsPerMinute", "admissionTtlSeconds") {
		return Input{}, ErrInput
	}
	totp, ok := root["totp"].(map[string]any)
	if !ok || !keys(totp, "mode", "enabled") {
		return Input{}, ErrInput
	}
	var in Input
	if json.Unmarshal(b, &in) != nil {
		return Input{}, ErrInput
	}
	if _, err = Build(in); err != nil {
		return Input{}, err
	}
	return in, nil
}

func decodeObject(r io.Reader) ([]byte, map[string]any, error) {
	b, err := io.ReadAll(io.LimitReader(r, MaxInputBytes+1))
	if err != nil || len(b) > MaxInputBytes {
		return nil, nil, ErrInput
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	v, err := value(d, 0)
	if err != nil {
		return nil, nil, ErrInput
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, nil, ErrInput
	}
	root, ok := v.(map[string]any)
	if !ok {
		return nil, nil, ErrInput
	}
	return b, root, nil
}

func keys(m map[string]any, names ...string) bool {
	if len(m) != len(names) {
		return false
	}
	for _, n := range names {
		if _, ok := m[n]; !ok {
			return false
		}
	}
	return true
}

func value(d *json.Decoder, depth int) (any, error) {
	if depth > 4 {
		return nil, ErrInput
	}
	t, err := d.Token()
	if err != nil || t == nil {
		return nil, ErrInput
	}
	if delim, ok := t.(json.Delim); ok {
		if delim != '{' {
			return nil, ErrInput
		}
		m := map[string]any{}
		for d.More() {
			k, err := d.Token()
			if err != nil {
				return nil, ErrInput
			}
			s, ok := k.(string)
			if !ok {
				return nil, ErrInput
			}
			if _, exists := m[s]; exists {
				return nil, ErrInput
			}
			v, err := value(d, depth+1)
			if err != nil {
				return nil, err
			}
			m[s] = v
		}
		end, err := d.Token()
		if err != nil || end != json.Delim('}') {
			return nil, ErrInput
		}
		return m, nil
	}
	return t, nil
}
