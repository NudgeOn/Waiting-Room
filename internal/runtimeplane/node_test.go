// SPDX-License-Identifier: Apache-2.0
package runtimeplane

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"waiting-room/internal/control"
)

type healthRoundTripper func(*http.Request) (*http.Response, error)

func (f healthRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func healthRooms(count int, transport http.RoundTripper) []*roomHandler {
	rooms := make([]*roomHandler, count)
	for i := range rooms {
		rooms[i] = &roomHandler{
			room:    control.Room{ID: fmt.Sprintf("room-%d", i), HealthURL: "https://origin.example.test/health"},
			runtime: control.Runtime{Revision: int64(i + 1), Epoch: 1, Mode: "HOLD"},
			client:  &http.Client{Transport: transport}, arrivals: &arrivalWindow{},
		}
	}
	return rooms
}

func TestGatewayHealthChecksBoundConcurrencyAndPreserveRoomOrder(t *testing.T) {
	var active, peak, calls atomic.Int32
	entered := make(chan struct{}, 100)
	release := make(chan struct{})
	transport := healthRoundTripper(func(r *http.Request) (*http.Response, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		calls.Add(1)
		entered <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
		return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
	})
	rooms := healthRooms(100, transport)
	done := make(chan struct{})
	go func() {
		defer close(done)
		metrics := probeGatewayRooms(context.Background(), rooms)
		for i, m := range metrics {
			if m.RoomID != rooms[i].room.ID || m.Revision != rooms[i].runtime.Revision || !m.OriginHealthy {
				t.Errorf("wrong metric at %d: %+v", i, m)
			}
		}
	}()
	for range 8 {
		select {
		case <-entered:
		case <-time.After(time.Second):
			close(release)
			<-done
			t.Fatal("health probes did not run concurrently")
		}
	}
	close(release)
	<-done
	if peak.Load() != 8 || calls.Load() != 100 {
		t.Fatalf("peak=%d calls=%d", peak.Load(), calls.Load())
	}
}

func TestGatewayCanceledHealthBudgetDoesNotProbeRemainingRoomsOrReportHealthy(t *testing.T) {
	var calls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	transport := healthRoundTripper(func(r *http.Request) (*http.Response, error) {
		if calls.Add(1) == 8 {
			cancel()
		}
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	defer cancel()
	metrics := probeGatewayRooms(ctx, healthRooms(100, transport))
	if calls.Load() != 8 || len(metrics) != 100 {
		t.Fatalf("calls=%d metrics=%d", calls.Load(), len(metrics))
	}
	for i, m := range metrics {
		if m.OriginHealthy || m.RoomID != fmt.Sprintf("room-%d", i) {
			t.Fatalf("canceled probe has incorrect metric: %+v", m)
		}
	}
}

func TestArrivalWindowRequiresFullObservationAndRejectsRollback(t *testing.T) {
	a := arrivalWindow{started: 1000}
	a.observe(time.Unix(1001, 0))
	a.observe(time.Unix(1200, 0))
	if n, ready := a.snapshot(time.Unix(1299, 0)); n != 2 || ready {
		t.Fatal("early full window", n, ready)
	}
	if n, ready := a.snapshot(time.Unix(1301, 0)); n != 1 || !ready {
		t.Fatal("window boundary", n, ready)
	}
	if _, ready := a.snapshot(time.Unix(1199, 0)); ready {
		t.Fatal("clock rollback claimed full observation")
	}
	a.observe(time.Unix(900, 0))
	if n, ready := a.snapshot(time.Unix(901, 0)); n != 1 || ready {
		t.Fatal("rollback failed reset", n, ready)
	}
}
func TestTargetSelectionHonorsRoomAndReservedBoundaries(t *testing.T) {
	room := control.Room{ID: "sale", Hostname: "shop.example.test", Active: true, ProtectPrefixes: []string{"/shop"}, ExcludePrefixes: []string{"/shop/status"}}
	other := control.Room{ID: "other", Hostname: room.Hostname, Active: true, ProtectPrefixes: []string{"/other"}}
	config := control.Config{Rooms: []control.Room{room, other}}
	for _, target := range []string{"/shop", "/shop/cart?source=app"} {
		if !targetFor(config, room, target) {
			t.Errorf("valid target rejected: %s", target)
		}
	}
	for _, target := range []string{"//evil.test/shop", "https://evil.test/shop", "/shopping", "/shop/status", "/other", "/_wr/v1/tickets", "/shop\\evil", "/shop\x00", "/shop#fragment"} {
		if targetFor(config, room, target) {
			t.Errorf("unsafe/cross-room target accepted: %q", target)
		}
	}
}
