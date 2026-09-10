// SPDX-License-Identifier: Apache-2.0
package valkeystore

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"waiting-room/internal/queue/model"
)

// RecoveryState contains server-observed, shared fencing state, never ticket IDs
// or credentials. The diagnostic reason is a fixed enum from the library.
type RecoveryState struct {
	Now             int64  `json:"now"`
	Primary         string `json:"primary"`
	Fence           uint64 `json:"fence"`
	Mode            string `json:"mode"`
	UnsafeUntil     int64  `json:"unsafeUntil"`
	Reason          string `json:"reason"`
	ValidationError string `json:"validationError"`
}

func decodeRecovery(raw string) (RecoveryState, error) {
	var state RecoveryState
	if json.Unmarshal([]byte(raw), &state) != nil || state.Now <= 0 || len(state.Primary) != 40 || state.Fence < 1 || state.Fence >= 9007199254740990 || (state.Mode != "ACTIVE" && state.Mode != "RECOVERY_HOLD") || (state.Mode == "RECOVERY_HOLD" && state.UnsafeUntil <= 0) {
		return state, ErrSchema
	}
	return state, nil
}

// MaintainRecovery is a bounded background step. It never bypasses unsafeUntil
// or repairs failed invariants. All coordinators obtain the same server record.
func (s *Store) MaintainRecovery(ctx context.Context) (RecoveryState, error) {
	if !s.runtime {
		return RecoveryState{}, ErrSchema
	}
	if err := ctx.Err(); err != nil {
		return RecoveryState{}, err
	}
	s.recoveryMu.Lock()
	defer s.recoveryMu.Unlock()
	primary, err := s.primaryID(ctx)
	if err != nil {
		return s.recovery, model.ErrUnavailable
	}
	incident := s.uncertainty.Load()
	reason := "observe"
	if incident > s.acknowledgedUncertainty.Load() {
		reason = "uncertain"
	}
	raw, err := s.client.Do(ctx, s.client.B().Fcall().Function(s.runtimeFunction("recover")).Numkeys(int64(len(s.keys))).Key(s.keys...).Arg(primary, reason).Build()).ToString()
	if err != nil {
		return s.recovery, model.ErrUnavailable
	}
	state, err := decodeRecovery(raw)
	if err != nil {
		return s.recovery, err
	}
	s.recovery = state
	// Clear local uncertainty only after the shared fence is acknowledged. A
	// shared RECOVERY_HOLD still rejects admission in both client and function.
	s.acknowledgedUncertainty.Store(incident)
	if state.Mode == "RECOVERY_HOLD" && state.Now >= state.UnsafeUntil && state.ValidationError == "" {
		raw, err = s.client.Do(ctx, s.client.B().Fcall().Function(s.runtimeFunction("validate")).Numkeys(int64(len(s.keys))).Key(s.keys...).Arg(state.Primary, strconv.FormatUint(state.Fence, 10)).Build()).ToString()
		if err != nil {
			return s.recovery, model.ErrUnavailable
		}
		state, err = decodeRecovery(raw)
		if err != nil {
			return s.recovery, err
		}
		s.recovery = state
	}
	return state, nil
}
func (s *Store) runtimeCall(ctx context.Context, read bool, args ...string) (Result, error) {
	entered := time.Now()
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if s.uncertainty.Load() > s.acknowledgedUncertainty.Load() {
		return Result{}, model.ErrUnavailable
	}
	s.recoveryMu.Lock()
	state := s.recovery
	s.recoveryMu.Unlock()
	lockWait := time.Since(entered)
	// Recovery performs bounded network I/O while holding this mutex. A caller
	// can expire while waiting even though its initial check succeeded. No RPC
	// has been submitted yet, so cancellation here is not an uncertain write.
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	metrics := read && len(args) == 1 && args[0] == "metrics"
	maintenance := read && len(args) == 1 && args[0] == "promotion-needed"
	heldConfig := s.runtimeVersion >= 5 && state.Mode == "RECOVERY_HOLD" && state.Reason == "epoch_reset" && !read && len(args) == 5 && args[0] == "configure"
	if state.Mode != "ACTIVE" && !metrics && !heldConfig {
		return Result{}, model.ErrUnavailable
	}
	operation := "command"
	if heldConfig {
		operation = "configure_held"
	}
	if read {
		operation = "status"
		if len(args) == 0 {
			operation = "capacity"
		}
		if metrics {
			operation = "metrics"
			args = nil
		}
		if maintenance {
			operation = "promotion-needed"
			args = nil
		}
	}
	values := append(append([]string{}, args...), state.Primary, strconv.FormatUint(state.Fence, 10))
	var raw string
	var err error
	began := time.Now()
	budget := -time.Millisecond
	if deadline, ok := ctx.Deadline(); ok {
		budget = deadline.Sub(began)
	}
	diagnosticOperation := operation
	if !read && len(args) > 0 {
		diagnosticOperation = args[0]
	}
	if read {
		function := s.runtimeFunction(operation)
		if maintenance {
			function = "wr_qm1_needed"
		}
		raw, err = s.client.Do(ctx, s.client.B().FcallRo().Function(function).Numkeys(int64(len(s.keys))).Key(s.keys...).Arg(values...).Build()).ToString()
	} else {
		raw, err = s.client.Do(ctx, s.client.B().Fcall().Function(s.runtimeFunction(operation)).Numkeys(int64(len(s.keys))).Key(s.keys...).Arg(values...).Build()).ToString()
	}
	if err != nil {
		// A fenced request has definitely not written, unlike transport loss.
		if strings.Contains(err.Error(), "WR_FENCED") {
			return Result{}, model.ErrUnavailable
		}
		if read && ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		if read && readTransportFailure(err) {
			return Result{}, model.ErrUnavailable
		}
		known := classify(err)
		if known != err && !errors.Is(known, ErrSchema) && !errors.Is(known, model.ErrUnavailable) {
			return Result{}, known
		}
		s.diagnoseCall(diagnosticOperation, read, err, began, lockWait, budget)
		s.uncertainty.Add(1)
		repair, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, _ = s.MaintainRecovery(repair)
		cancel()
		return Result{}, model.ErrUnavailable
	}
	var result Result
	if json.Unmarshal([]byte(raw), &result) != nil || (maintenance && (result.MaintenanceNeeded == nil || result.Now <= 0)) {
		s.diagnoseCall(diagnosticOperation, read, nil, began, lockWait, budget)
		s.uncertainty.Add(1)
		return Result{}, model.ErrUnavailable
	}
	if metrics && result.Metrics != nil {
		result.Metrics.RecoveryFence = state.Fence
		result.Metrics.RecoveryReason = state.Reason
		result.Metrics.RecoveryValidation = state.ValidationError
	}
	return result, nil
}
