//go:build integration

// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"context"
	"sync"
	"testing"
	"time"
	"waiting-room/internal/adminauth"
)

func TestRecoveryAuditDeduplicatesAtomicObservations(t *testing.T) {
	f, s, _ := publicationFixture(t)
	ctx := context.Background()
	envelope, err := s.Envelope(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ack := NodeAck{Generation: 1, Digest: digest(envelope), Rooms: []RoomMetrics{{RoomID: "sale", Revision: 1, Epoch: 1, Mode: "RECOVERY_HOLD", RecoveryFence: 2, RecoveryReason: "primary_changed", RecoveryUntil: time.Now().Add(90 * time.Second).UnixMilli()}}}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Acknowledge(ctx, "coordinator", ack); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	var count int
	if f.pool.QueryRow(ctx, "SELECT count(*) FROM control_audit WHERE action='runtime.recovery'").Scan(&count) != nil || count != 1 {
		t.Fatal("duplicate observation audit", count)
	}
	ack.Rooms[0].Mode = "HOLD"
	ack.Rooms[0].RecoveryUntil = 0
	execSQL(t, f.pool, "ALTER TABLE control_audit ADD CONSTRAINT simulated_recovery_audit CHECK(action<>'runtime.recovery') NOT VALID")
	if err = s.Acknowledge(ctx, "coordinator", ack); err != adminauth.ErrAuthUnavailable {
		t.Fatal("audit failure ACK committed", err)
	}
	var mode string
	if f.pool.QueryRow(ctx, "SELECT metrics->0->>'mode' FROM control_nodes WHERE node_id='coordinator'").Scan(&mode) != nil || mode != "RECOVERY_HOLD" {
		t.Fatal("failed audit overwrote prior node observation")
	}
	execSQL(t, f.pool, "ALTER TABLE control_audit DROP CONSTRAINT simulated_recovery_audit")
	if err = s.Acknowledge(ctx, "coordinator", ack); err != nil {
		t.Fatal(err)
	}
	if f.pool.QueryRow(ctx, "SELECT count(*) FROM control_audit WHERE action='runtime.recovery'").Scan(&count) != nil || count != 2 {
		t.Fatal("missing resume observation", count)
	}
	ack.Rooms[0].RecoveryReason = "untrusted raw diagnostic"
	if err = s.Acknowledge(ctx, "coordinator", ack); err == nil {
		t.Fatal("unbounded diagnostic accepted")
	}
}
