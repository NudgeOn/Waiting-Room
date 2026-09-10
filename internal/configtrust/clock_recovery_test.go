// SPDX-License-Identifier: Apache-2.0
package configtrust

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClockRollbackRequiresFreshDurableSignedRecovery(t *testing.T) {
	pub, key, s := fixture(t)
	store := &MemoryStore{}
	g := openTest(t, pub, store)
	old := signed(t, key, s)
	if err := g.applyAt(old, time.Unix(1005, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := g.currentAt(time.Unix(1003, 0)); err != ErrUnavailable {
		t.Fatal("rollback served")
	}
	if _, err := g.currentAt(time.Unix(1006, 0)); err != ErrUnavailable {
		t.Fatal("clock catch-up alone reopened")
	}
	if err := g.applyAt(old, time.Unix(1006, 0)); err != ErrUnavailable {
		t.Fatal("replayed LKG reopened", err)
	}
	s.Generation++
	s.IssuedAt = 1004
	if err := g.applyAt(signed(t, key, s), time.Unix(1006, 0)); err != ErrUnavailable {
		t.Fatal("pre-rollback publication reopened", err)
	}
	if err := g.applyAt([]byte("untrusted"), time.Unix(1006, 0)); err == nil {
		t.Fatal("untrusted refresh reopened")
	}
	s.IssuedAt = 1005
	if err := g.applyAt(signed(t, key, s), time.Unix(1006, 0)); err != nil {
		t.Fatal("fresh signed recovery remained permanently closed", err)
	}
	current, err := g.currentAt(time.Unix(1006, 0))
	if err != nil || current.Generation != 2 || g.FailureReason() != "" {
		t.Fatal("recovery not applied", err)
	}
	restored := openTest(t, pub, store)
	if err := restored.applyAt(old, time.Unix(1006, 0)); !errors.Is(err, ErrRollback) {
		t.Fatal("recovery lost durable high-water", err)
	}
}

func TestClockRecoveryBoundaryAdvancesOnRepeatedRollback(t *testing.T) {
	pub, key, s := fixture(t)
	g := openTest(t, pub, &MemoryStore{})
	if err := g.applyAt(signed(t, key, s), time.Unix(1005, 0)); err != nil {
		t.Fatal(err)
	}
	for _, sec := range []int64{1003, 1010, 1008} {
		if _, err := g.currentAt(time.Unix(sec, 0)); err != ErrUnavailable {
			t.Fatal("quarantine reopened")
		}
	}
	s.Generation++
	s.IssuedAt = 1009
	if err := g.applyAt(signed(t, key, s), time.Unix(1011, 0)); err != ErrUnavailable {
		t.Fatal("second rollback boundary ignored", err)
	}
	s.IssuedAt = 1010
	if err := g.applyAt(signed(t, key, s), time.Unix(1011, 0)); err != nil {
		t.Fatal("second safe recovery failed", err)
	}
}

func TestClockRecoveryNeverResurrectsExpiredSnapshot(t *testing.T) {
	pub, key, s := fixture(t)
	g := openTest(t, pub, &MemoryStore{})
	s.ExpiresAt = 1007
	old := signed(t, key, s)
	if err := g.applyAt(old, time.Unix(1005, 0)); err != nil {
		t.Fatal(err)
	}
	for _, sec := range []int64{1008, 1006, 1009} {
		if _, err := g.currentAt(time.Unix(sec, 0)); err != ErrUnavailable {
			t.Fatal("expired config resurrected")
		}
	}
	if err := g.applyAt(old, time.Unix(1009, 0)); err == nil {
		t.Fatal("expired replay recovered")
	}
	s.Generation++
	s.IssuedAt, s.ExpiresAt = 1008, 2000
	if err := g.applyAt(signed(t, key, s), time.Unix(1009, 0)); err != nil {
		t.Fatal("fresh refresh failed", err)
	}
}

func TestClockRecoveryHTTPGateStaysClosedUntilPublication(t *testing.T) {
	pub, key, s := fixture(t)
	g := openTest(t, pub, &MemoryStore{})
	sec := int64(1005)
	g.now = func() time.Time { return time.Unix(sec, 0) }
	if err := g.Apply(signed(t, key, s)); err != nil {
		t.Fatal(err)
	}
	calls := 0
	h := g.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(204) }))
	probe := func(want int) {
		t.Helper()
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/_wr/v1/tickets", nil))
		if w.Code != want {
			t.Fatalf("got %d, want %d", w.Code, want)
		}
	}
	probe(204)
	sec = 1003
	probe(503)
	sec = 1006
	probe(503)
	s.Generation++
	s.IssuedAt = 1006
	if err := g.Apply(signed(t, key, s)); err != nil {
		t.Fatal(err)
	}
	probe(204)
	if calls != 2 {
		t.Fatal("quarantine allowed handler execution")
	}
}

func TestClockRecoveryPersistenceFailureStillLatchesClosed(t *testing.T) {
	pub, key, s := fixture(t)
	g := openTest(t, pub, &MemoryStore{})
	if err := g.applyAt(signed(t, key, s), time.Unix(1005, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := g.currentAt(time.Unix(1003, 0)); err != ErrUnavailable {
		t.Fatal("rollback served")
	}
	g.store = &failingStore{}
	s.Generation++
	s.IssuedAt = 1006
	if err := g.applyAt(signed(t, key, s), time.Unix(1006, 0)); err != ErrPersistence {
		t.Fatal("uncertain recovery write ignored", err)
	}
	g.store = &MemoryStore{}
	s.Generation++
	s.IssuedAt = 1007
	if err := g.applyAt(signed(t, key, s), time.Unix(1007, 0)); err != ErrUnavailable || g.FailureReason() != "snapshot_persistence" {
		t.Fatal("fresh publication repaired uncertain persistence", err)
	}
}
