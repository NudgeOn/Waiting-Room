// SPDX-License-Identifier: Apache-2.0
// Package valkeystore is the M1 single-room store. It is not a qualified HA runtime.
package valkeystore

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	valkey "github.com/valkey-io/valkey-go"
	"waiting-room/internal/queue/model"
)

//go:embed functions.lua
var library string

//go:embed installation_v2.lua
var installationLibrary string

//go:embed runtime_v3.lua
var runtimeLibrary string

//go:embed runtime_v4.lua
var runtimeLibraryV4 string

//go:embed runtime_v5.lua
var runtimeLibraryV5 string

//go:embed runtime_v6.lua
var runtimeLibraryV6 string

var ErrSweep = errors.New("bounded expiry sweep required")
var ErrSchema = errors.New("store schema or configuration mismatch")

type JoinSnapshot struct {
	Ticket  model.Ticket `json:"ticket"`
	IdleTTL int64        `json:"idleTTL"`
}

type Result struct {
	MaintenanceNeeded *bool          `json:"maintenanceNeeded,omitempty"`
	Join              *JoinSnapshot  `json:"join,omitempty"`
	Now               int64          `json:"now"`
	Ticket            *model.Ticket  `json:"ticket"`
	Tickets           []model.Ticket `json:"tickets"`
	Replay            string         `json:"replay"`
	Capacity          *Capacity      `json:"capacity,omitempty"`
	Metrics           *Metrics       `json:"metrics,omitempty"`
}

// Empty Lua arrays encode as {}; normalize only this protocol field.
func (r *Result) UnmarshalJSON(b []byte) error {
	type wire struct {
		MaintenanceNeeded *bool
		Join              *JoinSnapshot
		Now               int64
		Ticket            *model.Ticket
		Tickets           json.RawMessage
		Replay            string
		Capacity          *Capacity
		Metrics           *Metrics
	}
	var w wire
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	r.Now, r.Ticket, r.Replay = w.Now, w.Ticket, w.Replay
	r.MaintenanceNeeded = w.MaintenanceNeeded
	r.Join = w.Join
	r.Capacity = w.Capacity
	r.Metrics = w.Metrics
	if len(w.Tickets) > 0 && string(w.Tickets) != "{}" {
		return json.Unmarshal(w.Tickets, &r.Tickets)
	}
	return nil
}

type Store struct {
	diagnostic              callDiagnostic
	runtimeVersion          int
	epoch                   uint64
	client                  valkey.Client
	keys                    []string
	primary                 string
	failed                  atomic.Bool
	installation            bool
	runtime                 bool
	recoveryMu              sync.Mutex
	recovery                RecoveryState
	uncertainty             atomic.Uint64
	acknowledgedUncertainty atomic.Uint64
}

func Hash(value string) string { h := sha256.Sum256([]byte(value)); return hex.EncodeToString(h[:]) }

// Open never replaces an existing library or overwrites an existing configuration.
// Only isolated lab namespaces are accepted until installation-wide counters exist.
func Open(ctx context.Context, address, namespace string, config model.Config) (*Store, error) {
	if !regexp.MustCompile(`^wr:lab:[a-zA-Z0-9_-]{1,80}$`).MatchString(namespace) {
		return nil, ErrSchema
	}
	if _, err := model.New(config); err != nil {
		return nil, err
	}
	c, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{address}, ForceSingleClient: true, DisableCache: true, DisableRetry: true})
	if err != nil {
		return nil, err
	}
	s := &Store{client: c}
	for _, suffix := range []string{"meta", "tickets", "waiting", "expiry", "leases", "rate", "idempotency", "idem-expiry"} {
		s.keys = append(s.keys, namespace+"{room:1}:"+suffix)
	}
	success := false
	defer func() {
		if !success {
			c.Close()
		}
	}()
	s.primary, err = s.primaryID(ctx)
	if err != nil {
		return nil, err
	}
	if err = s.load(ctx); err != nil {
		return nil, err
	}
	data, _ := json.Marshal(config)
	err = c.Do(ctx, c.B().Fcall().Function("wr_v1_init").Numkeys(8).Key(s.keys...).Arg(string(data), s.primary).Build()).Error()
	if err != nil {
		return nil, classify(err)
	}
	success = true
	return s, nil
}
func (s *Store) Close() { s.client.Close() }
func (s *Store) load(ctx context.Context) error {
	source, name := library, "wr_queue_v1"
	if s.installation {
		source, name = installationLibrary, "wr_queue_install_v2"
	}
	err := s.client.Do(ctx, s.client.B().FunctionLoad().FunctionCode(source).Build()).Error()
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return err
	}
	entries, err := s.client.Do(ctx, s.client.B().FunctionList().Libraryname(name).Withcode().Build()).ToArray()
	if err != nil {
		return err
	}
	if len(entries) != 1 {
		return ErrSchema
	}
	fields, err := entries[0].AsMap()
	if err != nil {
		return err
	}
	code := fields["library_code"]
	actual, err := code.ToString()
	if err != nil || actual != source {
		return ErrSchema
	}
	return nil
}
func (s *Store) primaryID(ctx context.Context) (string, error) {
	info, err := s.client.Do(ctx, s.client.B().Info().Section("server").Build()).ToString()
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(info, "\r\n") {
		if strings.HasPrefix(line, "run_id:") {
			return strings.TrimPrefix(line, "run_id:"), nil
		}
	}
	return "", ErrSchema
}
func classify(err error) error {
	if err == nil {
		return nil
	}
	for key, value := range map[string]error{"WR_CAPACITY": model.ErrCapacity, "WR_CONFLICT": model.ErrConflict, "WR_EXPIRED": model.ErrExpired, "WR_DRAIN": model.ErrDrain, "WR_NOT_READY": model.ErrNotReady, "WR_UNAVAILABLE": model.ErrUnavailable, "WR_SWEEP_REQUIRED": ErrSweep, "WR_SCHEMA": ErrSchema} {
		if strings.Contains(err.Error(), key) {
			return value
		}
	}
	return err
}
func (s *Store) call(ctx context.Context, read bool, args ...string) (Result, error) {
	if s.runtime {
		return s.runtimeCall(ctx, read, args...)
	}
	if s.failed.Load() {
		return Result{}, model.ErrUnavailable
	}
	// A canceled caller is not evidence of a primary failure. In particular, a
	// disconnected browser must not poison unrelated visitors before any RPC.
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	primary, err := s.primaryID(ctx)
	if err != nil && ctx.Err() != nil {
		return Result{}, ctx.Err()
	}
	if err != nil && readTransportFailure(err) {
		return Result{}, model.ErrUnavailable
	}
	if err != nil || primary != s.primary {
		s.failed.Store(true)
		return Result{}, model.ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	var raw string
	prefix := "wr_v1_"
	if s.installation {
		prefix = "wr_i2_"
	}
	if s.runtime {
		prefix = "wr_r3_"
	}
	if read {
		operation := "status"
		if s.installation && len(args) == 0 {
			operation = "capacity"
		}
		if s.runtime && len(args) == 1 && args[0] == "metrics" {
			operation = "metrics"
			args = nil
		}
		raw, err = s.client.Do(ctx, s.client.B().FcallRo().Function(prefix+operation).Numkeys(int64(len(s.keys))).Key(s.keys...).Arg(args...).Build()).ToString()
	} else {
		raw, err = s.client.Do(ctx, s.client.B().Fcall().Function(prefix+"command").Numkeys(int64(len(s.keys))).Key(s.keys...).Arg(args...).Build()).ToString()
	}
	if err != nil {
		// FCALL_RO cannot partially mutate queue state. Each subsequent request
		// independently verifies primary identity. Write uncertainty still latches.
		if read && ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		if read && readTransportFailure(err) {
			return Result{}, model.ErrUnavailable
		}
		known := classify(err)
		// An unknown function/write error may follow a partial script write. Latch closed.
		if known == err || errors.Is(known, ErrSchema) || errors.Is(known, model.ErrUnavailable) {
			s.failed.Store(true)
			return Result{}, model.ErrUnavailable
		}
		return Result{}, known
	}
	var result Result
	if err = json.Unmarshal([]byte(raw), &result); err != nil {
		s.failed.Store(true)
		return Result{}, model.ErrUnavailable
	}
	return result, nil
}
func (s *Store) Join(ctx context.Context, key, fingerprint, ticketHash, replay string) (Result, error) {
	return s.call(ctx, false, "join", Hash(key), Hash(fingerprint), ticketHash, replay)
}
func (s *Store) Status(ctx context.Context, id string) (Result, error) { return s.call(ctx, true, id) }
func (s *Store) Claim(ctx context.Context, id string) (Result, error) {
	return s.call(ctx, false, "claim", id)
}
func (s *Store) Heartbeat(ctx context.Context, id string) (Result, error) {
	return s.call(ctx, false, "heartbeat", id)
}
func (s *Store) Promote(ctx context.Context, n int) (Result, error) {
	if n < 1 || n > 128 {
		return Result{}, fmt.Errorf("batch must be 1..128")
	}
	if s.runtime && s.runtimeVersion >= 6 {
		probe, err := s.runtimeCall(ctx, true, "promotion-needed")
		if err != nil {
			return Result{}, err
		}
		if !*probe.MaintenanceNeeded {
			return Result{Now: probe.Now, Tickets: []model.Ticket{}}, nil
		}
	}
	return s.call(ctx, false, "promote", fmt.Sprint(n))
}
func (s *Store) Mode(ctx context.Context, mode string) (Result, error) {
	if mode != "hold" && mode != "auto" && mode != "drain" {
		return Result{}, ErrSchema
	}
	return s.call(ctx, false, mode)
}
func (s *Store) Sweep(ctx context.Context) (Result, error) { return s.call(ctx, false, "sweep") }
