// SPDX-License-Identifier: Apache-2.0
package runtimeplane

import (
	"context"
	"testing"
	"time"
	"waiting-room/internal/configtrust"
	"waiting-room/internal/control"
)

func TestUnchangedConfigRefreshDoesNotWaitForActiveRequests(t *testing.T) {
	d := control.Delivery{Config: control.Config{SchemaVersion: 1, Revision: 1, Profile: "standard-10k", RegionID: "local", Rooms: []control.Room{}}, Runtimes: []control.RoomRuntime{}}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	n := &Node{generation: 7}
	// Requests and maintenance hold this shared lock during bounded network I/O.
	// An already applied signed generation must not request an exclusive lock.
	n.mu.RLock()
	done := make(chan error, 1)
	go func() { done <- n.apply(context.Background(), configtrust.Snapshot{Generation: 7, Payload: d.Bytes()}) }()
	select {
	case err := <-done:
		n.mu.RUnlock()
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(300 * time.Millisecond):
		n.mu.RUnlock()
		<-done
		t.Fatal("unchanged configuration waited behind active request I/O")
	}
}

func TestUnchangedGenerationStillRejectsInvalidDelivery(t *testing.T) {
	n := &Node{generation: 7}
	if err := n.apply(context.Background(), configtrust.Snapshot{Generation: 7, Payload: []byte(`{"config":{}}`)}); err != control.ErrInvalid {
		t.Fatal("generation fast path bypassed payload validation", err)
	}
}

func TestChangedConfigStillWaitsForActiveRequests(t *testing.T) {
	d := control.Delivery{Config: control.Config{SchemaVersion: 1, Revision: 2, Profile: "standard-10k", RegionID: "local", Rooms: []control.Room{}}, Runtimes: []control.RoomRuntime{}}
	n := &Node{generation: 7}
	n.mu.RLock()
	done := make(chan error, 1)
	go func() { done <- n.apply(context.Background(), configtrust.Snapshot{Generation: 8, Payload: d.Bytes()}) }()
	select {
	case err := <-done:
		n.mu.RUnlock()
		t.Fatal("changed generation bypassed active request lifetime", err)
	case <-time.After(50 * time.Millisecond):
	}
	n.mu.RUnlock()
	select {
	case err := <-done:
		if err != nil || n.generation != 8 || n.delivery.Config.Revision != 2 {
			t.Fatal("new config did not apply after active requests drained", err)
		}
	case <-time.After(time.Second):
		t.Fatal("new configuration did not finish")
	}
}
