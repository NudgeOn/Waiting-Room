// SPDX-License-Identifier: Apache-2.0
package configtrust

import (
	"testing"
	"time"
)

func TestLatchedFailureReasonPreservesCauseAndTrustBoundary(t *testing.T) {
	pub, key, snapshot := fixture(t)
	raw := signed(t, key, snapshot)
	g := openTest(t, pub, &MemoryStore{})
	if g.FailureReason() != "" || g.applyAt(raw, time.Unix(1001, 0)) != nil {
		t.Fatal("valid initial gate")
	}
	if g.applyAt([]byte("untrusted private payload"), time.Unix(1001, 0)) != ErrInvalid || g.FailureReason() != "" {
		t.Fatal("invalid incoming data poisoned last-known-good")
	}
	if _, err := g.currentAt(time.Unix(1001, 0)); err != nil {
		t.Fatal("valid last-known-good unavailable")
	}
	if _, err := g.currentAt(time.Unix(1000, 0)); err != ErrUnavailable || g.FailureReason() != "snapshot_clock_rollback" {
		t.Fatal("clock rollback cause lost")
	}
	snapshot.Generation++
	if g.applyAt(signed(t, key, snapshot), time.Unix(1002, 0)) != ErrUnavailable || g.FailureReason() != "snapshot_clock_rollback" {
		t.Fatal("reading cause or new config reopened failed trust")
	}
	failed := openTest(t, pub, &failingStore{})
	if failed.applyAt(raw, time.Unix(1001, 0)) != ErrPersistence {
		t.Fatal("expected failed persistence")
	}
	if _, err := failed.currentAt(time.Unix(1001, 0)); err != ErrUnavailable || failed.FailureReason() != "snapshot_persistence" {
		t.Fatal("Current obscured persistence cause")
	}
}
