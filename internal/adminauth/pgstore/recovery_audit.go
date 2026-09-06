// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"waiting-room/internal/adminauth"
)

//go:embed migrations/010_system_audit.sql
var Migration010 string

type recoveryObservation struct {
	RoomID     string `json:"roomId"`
	Epoch      uint64 `json:"epoch"`
	Fence      uint64 `json:"fence"`
	Hold       bool   `json:"hold"`
	Until      int64  `json:"until"`
	Reason     string `json:"reason"`
	Validation string `json:"validation"`
}

func recoveryObserved(m RoomMetrics) recoveryObservation {
	return recoveryObservation{m.RoomID, m.Epoch, m.RecoveryFence, m.Mode == "RECOVERY_HOLD", m.RecoveryUntil, m.RecoveryReason, m.RecoveryValidation}
}
func auditRecovery(ctx context.Context, tx pgx.Tx, node string, rooms []RoomMetrics) error {
	if node != "coordinator" {
		return nil
	}
	var raw []byte
	err := tx.QueryRow(ctx, "SELECT metrics FROM control_nodes WHERE node_id=$1 FOR UPDATE", node).Scan(&raw)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return adminauth.ErrAuthUnavailable
	}
	old := []RoomMetrics{}
	if len(raw) > 0 && json.Unmarshal(raw, &old) != nil {
		return adminauth.ErrAuthUnavailable
	}
	previous := map[string]RoomMetrics{}
	for _, m := range old {
		previous[m.RoomID] = m
	}
	for _, m := range rooms {
		before := recoveryObserved(previous[m.RoomID])
		after := recoveryObserved(m)
		completedBeforeObservation := !before.Hold && !after.Hold && after.Fence > before.Fence && after.Reason != ""
		if before == after || (!before.Hold && !after.Hold && before.Validation == after.Validation && !completedBeforeObservation) {
			continue
		}
		from, _ := json.Marshal(before)
		to, _ := json.Marshal(after)
		requestID, _, err := adminauth.NewCSRFToken()
		if err != nil {
			return err
		}
		outcome := "resumed"
		if completedBeforeObservation {
			outcome = "recovered_before_observation"
		}
		if after.Hold {
			outcome = "held"
		}
		if after.Validation != "" {
			outcome = "validation_failed"
		}
		if _, err = tx.Exec(ctx, "INSERT INTO control_audit(actor_id,actor_role,action,target_id,before_digest,after_digest,result,request_id,revision) VALUES('system:coordinator','system','runtime.recovery',$1,$2,$3,$4,$5,$6)", m.RoomID, digest(from), digest(to), outcome, requestID, m.Revision); err != nil {
			return adminauth.ErrAuthUnavailable
		}
	}
	return nil
}
