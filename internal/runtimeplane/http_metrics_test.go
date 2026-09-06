// SPDX-License-Identifier: Apache-2.0
package runtimeplane

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"waiting-room/internal/configtrust"
	"waiting-room/internal/control"
	"waiting-room/internal/localcontrol"
)

func TestHTTPErrorWindowUsesSixtyBucketsAndResetsOnRollback(t *testing.T) {
	w := httpErrorsFor(nil, control.Room{}, time.Unix(1000, 0))
	w.observe(time.Unix(1000, 0))
	w.observe(time.Unix(1001, 0))
	w.observe(time.Unix(1059, 0))
	for _, tc := range []struct {
		sec   int64
		count int64
		ready bool
	}{{1059, 3, false}, {1060, 2, true}, {1061, 1, true}, {1119, 0, true}, {1100, 0, false}, {1160, 0, true}} {
		if count, ready := w.snapshot(time.Unix(tc.sec, 0)); count != tc.count || ready != tc.ready {
			t.Fatalf("at %d: got (%d,%t), want (%d,%t)", tc.sec, count, ready, tc.count, tc.ready)
		}
	}
	w.observe(time.Unix(900, 0))
	if count, ready := w.snapshot(time.Unix(901, 0)); count != 1 || ready {
		t.Fatalf("observation rollback: count=%d ready=%t", count, ready)
	}
}

func TestHTTPErrorWindowCountsConcurrentResponses(t *testing.T) {
	w := httpErrorsFor(nil, control.Room{}, time.Unix(1000, 0))
	var workers sync.WaitGroup
	for range 20 {
		workers.Go(func() {
			for range 100 {
				w.observe(time.Unix(1001, 0))
			}
		})
	}
	workers.Wait()
	if count, _ := w.snapshot(time.Unix(1002, 0)); count != 2000 {
		t.Fatalf("lost concurrent errors: %d", count)
	}
}

func TestHTTPErrorWindowRetainsOnlyUnchangedRoomRouting(t *testing.T) {
	room := control.Room{Origin: "https://origin.test", Hostname: "shop.test", ProtectPrefixes: []string{"/shop"}, ExcludePrefixes: []string{"/shop/assets"}}
	window := httpErrorsFor(nil, room, time.Now())
	old := &roomHandler{room: room, httpErrors: window}
	if httpErrorsFor(old, room, time.Now()) != window {
		t.Fatal("unchanged routing lost observation")
	}
	for _, changed := range []control.Room{
		{Origin: "https://new.test", Hostname: room.Hostname, ProtectPrefixes: room.ProtectPrefixes, ExcludePrefixes: room.ExcludePrefixes},
		{Origin: room.Origin, Hostname: "new.test", ProtectPrefixes: room.ProtectPrefixes, ExcludePrefixes: room.ExcludePrefixes},
		{Origin: room.Origin, Hostname: room.Hostname, ProtectPrefixes: []string{"/other"}, ExcludePrefixes: room.ExcludePrefixes},
		{Origin: room.Origin, Hostname: room.Hostname, ProtectPrefixes: room.ProtectPrefixes},
	} {
		if httpErrorsFor(old, changed, time.Now()) == window {
			t.Fatal("changed routing retained prior errors")
		}
	}
}

func TestMeasuredRoomCountsOnlyFirstFinal5xxAndPreservesResponse(t *testing.T) {
	for _, tc := range []struct {
		name    string
		write   func(http.ResponseWriter)
		errors  int64
		flushed bool
	}{
		{"5xx", func(w http.ResponseWriter) { w.WriteHeader(503); _, _ = w.Write([]byte("unavailable")) }, 1, false},
		{"4xx", func(w http.ResponseWriter) { w.WriteHeader(429) }, 0, false},
		{"implicit 200", func(w http.ResponseWriter) { _, _ = w.Write([]byte("ok")); w.WriteHeader(503) }, 0, false},
		{"first final wins", func(w http.ResponseWriter) { w.WriteHeader(502); w.WriteHeader(503) }, 1, false},
		{"stream flush sends 200", func(w http.ResponseWriter) { _ = http.NewResponseController(w).Flush(); w.WriteHeader(503) }, 0, true},
		{"flushed 5xx", func(w http.ResponseWriter) { w.WriteHeader(500); w.(http.Flusher).Flush() }, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			window := httpErrorsFor(nil, control.Room{}, time.Now())
			out := httptest.NewRecorder()
			serveMeasuredRoom(out, httptest.NewRequest("GET", "https://shop.test/shop", nil), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("X-Origin", "retained")
				tc.write(w)
			}), window)
			if count, _ := window.snapshot(time.Now()); count != tc.errors {
				t.Fatalf("count=%d want %d", count, tc.errors)
			}
			if out.Header().Get("X-Origin") != "retained" || out.Flushed != tc.flushed {
				t.Fatalf("response semantics changed: header=%s flushed=%t", out.Header().Get("X-Origin"), out.Flushed)
			}
		})
	}
}

func TestMeasuredRoomInformationalHeadersDoNotHideFinalError(t *testing.T) {
	window := httpErrorsFor(nil, control.Room{}, time.Now())
	w := &httpErrorWriter{ResponseWriter: httptest.NewRecorder(), window: window}
	w.WriteHeader(103)
	w.WriteHeader(502)
	w.WriteHeader(503)
	if count, _ := window.snapshot(time.Now()); count != 1 {
		t.Fatalf("informational/final status counted incorrectly: %d", count)
	}
}

func TestGatewayMetricSeparatesRoomFailuresFromHealthProbeErrors(t *testing.T) {
	transport := healthRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
	})
	rooms := healthRooms(2, transport)
	for _, room := range rooms {
		room.httpErrors = httpErrorsFor(nil, room.room, time.Now().Add(-time.Minute))
	}
	serveMeasuredRoom(httptest.NewRecorder(), httptest.NewRequest("GET", "/shop", nil), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(502)
	}), rooms[0].httpErrors)
	metrics := probeGatewayRooms(context.Background(), rooms)
	for i, m := range metrics {
		if m.OriginHealthy || m.HTTP5xxLastMinute == nil || *m.HTTP5xxLastMinute != int64(1-i) || !m.HTTP5xxWindowReady {
			t.Fatalf("room %d has wrong health/error observation: %+v", i, m)
		}
	}
}

func TestGatewayRoutesAttributeErrorsOnlyToKnownRooms(t *testing.T) {
	first := control.Room{ID: "sale", PublicID: strings.Repeat("a", 20), Name: "Sale", Hostname: "shop.example.test", Origin: "https://origin.example.test", HealthURL: "https://origin.example.test/health", ProtectPrefixes: []string{"/shop"}, ExcludePrefixes: []string{"/shop/assets"}, QueuePolicy: control.QueuePolicy{Kind: "fifo", TicketIdleTTLSeconds: 600, TicketMaxTTLSeconds: 86400, ReadyTTLSeconds: 120}, Limits: control.Limits{MaxActiveAdmissionLeases: 1000, AdmissionsPerMinute: 600, AdmissionTTLSeconds: 900}, Theme: control.Theme{TemplateID: "calm", Title: "Waiting", Message: "Please wait", PrimaryColor: "#315b4a", Locale: "ko"}, Active: true}
	second := first
	second.ID, second.PublicID = "other", strings.Repeat("b", 20)
	second.ProtectPrefixes, second.ExcludePrefixes = []string{"/other"}, []string{}
	d := control.Delivery{Config: control.Config{SchemaVersion: 1, Revision: 1, Profile: "standard-10k", RegionID: "local", Rooms: []control.Room{first, second}}, Runtimes: []control.RoomRuntime{{RoomID: first.ID, Runtime: control.InitialRuntime(first)}, {RoomID: second.ID, Runtime: control.InitialRuntime(second)}}}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	gate, err := configtrust.Open(map[string]ed25519.PublicKey{"test": public}, "test", 1, control.ValidateDelivery, &configtrust.MemoryStore{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	raw, err := configtrust.Sign(private, configtrust.Snapshot{SchemaVersion: 1, Installation: "test", Generation: 1, Revision: 1, IssuedAt: now.Add(-time.Second).Unix(), ExpiresAt: now.Add(time.Minute).Unix(), Kid: "test", Payload: d.Bytes()})
	if err != nil || gate.Apply(raw) != nil {
		t.Fatal("valid route fixture did not apply", err)
	}
	node := &Node{identity: localcontrol.NodeIdentity{Node: "gateway"}, gate: gate, generation: 1, delivery: d, rooms: map[string]*roomHandler{}}
	for _, room := range d.Config.Rooms {
		node.rooms[room.PublicID] = &roomHandler{room: room, httpErrors: httpErrorsFor(nil, room, now.Add(-time.Minute)), handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }), origin: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(502) })}
	}
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", "/shop", "", 503},
		{"GET", "/other", "", 503},
		{"GET", "/shop/assets/logo.png", "", 502},
		{"GET", "/about", "", 502}, // General host traffic cannot be attributed to either Room.
		{"POST", "/_wr/v1/tickets", `{"target":"/shop"}`, 503},
		{"GET", "/_wr/v1/rooms/" + second.PublicID + "/admissions", "", 503},
		{"GET", "/_wr/v1/rooms/unknown/admissions", "", 404},
	} {
		response := httptest.NewRecorder()
		node.ServeHTTP(response, httptest.NewRequest(tc.method, "https://shop.example.test:20443"+tc.path, strings.NewReader(tc.body)))
		if response.Code != tc.status {
			t.Fatalf("%s returned %d, want %d", tc.path, response.Code, tc.status)
		}
	}
	for i, room := range d.Config.Rooms {
		if count, ready := node.rooms[room.PublicID].httpErrors.snapshot(time.Now()); count != int64(3-i) || !ready {
			t.Fatalf("%s error attribution: count=%d ready=%t", room.ID, count, ready)
		}
	}
}
