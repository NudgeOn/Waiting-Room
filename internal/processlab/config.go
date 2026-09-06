// SPDX-License-Identifier: Apache-2.0
package processlab

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"strings"
	"time"

	"waiting-room/internal/configtrust"
	"waiting-room/internal/lab"
	"waiting-room/internal/queue/model"
	"waiting-room/internal/queue/valkeystore"
)

// Full binding of the lab Gateway's fixed runtime inputs. Raw service/return
// secrets stay in private bootstrap IPC; only their hashes enter the snapshot.
type gatewayBinding struct {
	Kind            string `json:"kind"`
	Coordinator     string `json:"coordinator"`
	Origin          string `json:"origin"`
	Room            string `json:"room"`
	Audience        string `json:"audience"`
	Template        string `json:"template"`
	AdmissionKid    string `json:"admissionKid"`
	AdmissionPublic []byte `json:"admissionPublic"`
	ServiceHash     string `json:"serviceHash"`
	ReturnHash      string `json:"returnHash"`
}

func gatewayPayload(in gatewayInput) json.RawMessage {
	raw, _ := json.Marshal(gatewayBinding{"loopback-gateway-v1", in.Coordinator, in.Origin, lab.Room, lab.Audience, "calm", "lab-ephemeral", in.Public, valkeystore.Hash(in.Service), valkeystore.Hash(string(in.ReturnKey))})
	return raw
}

// The trusted lab supervisor acts as an ephemeral config signer. Production
// requires a separate Control signer and deployment-pinned trust; this is not it.
func signGatewayConfig(in gatewayInput, now time.Time, validity time.Duration) (gatewayInput, error) {
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return gatewayInput{}, ErrRole
	}
	snapshot := configtrust.Snapshot{SchemaVersion: 1, Installation: in.Installation, Generation: 1, Revision: 1, IssuedAt: now.Unix(), ExpiresAt: now.Add(validity).Unix(), Kid: "lab-config", Payload: gatewayPayload(in)}
	in.SignedConfig, err = configtrust.Sign(private, snapshot)
	if err != nil {
		return gatewayInput{}, ErrRole
	}
	in.ConfigTrust = pub
	return in, nil
}
func gatewayTrust(in gatewayInput) *configtrust.Gate {
	expected := gatewayPayload(in)
	validate := func(raw json.RawMessage) error {
		if !bytes.Equal(raw, expected) {
			return configtrust.ErrInvalid
		}
		return nil
	}
	g, err := configtrust.Open(map[string]ed25519.PublicKey{"lab-config": in.ConfigTrust}, in.Installation, 1, validate, &configtrust.MemoryStore{})
	if err != nil {
		return nil
	}
	_ = g.Apply(in.SignedConfig)
	return g
}

func coordinatorConfig() model.Config {
	cfg := model.DefaultConfig()
	cfg.LeaseCap = 3
	cfg.Rate = 6
	cfg.AdmissionTTL = 60000
	return cfg
}

type coordinatorBinding struct {
	Kind         string                         `json:"kind"`
	Address      string                         `json:"address"`
	Namespace    string                         `json:"namespace"`
	Room         string                         `json:"room"`
	Config       model.Config                   `json:"config"`
	Installation valkeystore.InstallationConfig `json:"installation"`
}

func coordinatorPayload(in coordinatorInput) json.RawMessage {
	raw, _ := json.Marshal(coordinatorBinding{"loopback-coordinator-v1", in.Address, in.Namespace, "room", coordinatorConfig(), valkeystore.StandardInstallation()})
	return raw
}
func signCoordinatorConfig(in coordinatorInput, now time.Time, validity time.Duration) (coordinatorInput, error) {
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return coordinatorInput{}, ErrRole
	}
	snapshot := configtrust.Snapshot{SchemaVersion: 1, Installation: strings.TrimPrefix(in.Namespace, "wr:lab:"), Generation: 1, Revision: 1, IssuedAt: now.Unix(), ExpiresAt: now.Add(validity).Unix(), Kid: "lab-config", Payload: coordinatorPayload(in)}
	in.SignedConfig, err = configtrust.Sign(private, snapshot)
	if err != nil {
		return coordinatorInput{}, ErrRole
	}
	in.ConfigTrust = pub
	return in, nil
}
func coordinatorTrust(in coordinatorInput) *configtrust.Gate {
	expected := coordinatorPayload(in)
	validate := func(raw json.RawMessage) error {
		if !bytes.Equal(raw, expected) {
			return configtrust.ErrInvalid
		}
		return nil
	}
	g, err := configtrust.Open(map[string]ed25519.PublicKey{"lab-config": in.ConfigTrust}, strings.TrimPrefix(in.Namespace, "wr:lab:"), 1, validate, &configtrust.MemoryStore{})
	if err != nil {
		return nil
	}
	_ = g.Apply(in.SignedConfig)
	return g
}
