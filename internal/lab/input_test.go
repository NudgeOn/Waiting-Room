// SPDX-License-Identifier: Apache-2.0
package lab

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"waiting-room/internal/queue/model"
	"waiting-room/internal/queue/valkeystore"
)

type inputQueue struct {
	Queue
	joins, reads int
}

func (q *inputQueue) Join(_ context.Context, _, _ string, id, replay string) (valkeystore.Result, error) {
	q.joins++
	now := time.Now().UnixMilli()
	return valkeystore.Result{Replay: replay, Ticket: &model.Ticket{ID: id, State: model.Waiting, JoinedAt: now, IdleUntil: now + 600000, AbsoluteUntil: now + 86400000}}, nil
}
func (q *inputQueue) Status(_ context.Context, id string) (valkeystore.Result, error) {
	q.reads++
	now := time.Now().UnixMilli()
	return valkeystore.Result{Ticket: &model.Ticket{ID: id, State: model.Waiting, JoinedAt: now, IdleUntil: now + 600000, AbsoluteUntil: now + 86400000}}, nil
}

func TestJoinInputBoundary(t *testing.T) {
	for _, tt := range []struct {
		name, content, body string
		status              int
	}{
		{"json", "application/json", `{"target":"/shop"}`, 202},
		{"charset", "application/json; charset=UTF-8", "  { \"target\" : \"/shop\" } \n", 202},
		{"case_insensitive_media", "Application/JSON", `{"target":"/shop"}`, 202},
		{"prefix_not_type", "application/json-invalid", `{"target":"/shop"}`, 400},
		{"wrong_charset", "application/json; charset=iso-8859-1", `{"target":"/shop"}`, 400},
		{"unknown_parameter", "application/json; ignored=yes", `{"target":"/shop"}`, 400},
		{"duplicate", "application/json", `{"target":"/shop","target":"/shop/item"}`, 400},
		{"case_field", "application/json", `{"Target":"/shop"}`, 400},
		{"escaped_duplicate", "application/json", `{"target":"/shop","\u0074arget":"/shop"}`, 400},
		{"null", "application/json", `{"target":null}`, 400},
		{"missing", "application/json", `{}`, 400},
		{"array", "application/json", `["/shop"]`, 400},
		{"invalid_utf8", "application/json", "{\"target\":\"/shop/\xff\"}", 400},
		{"invalid_utf8_query", "application/json", "{\"target\":\"/shop/?\x92\"}", 400},
		{"unpaired_surrogate", "application/json", `{"target":"/shop/\ud800"}`, 400},
		{"oversize", "application/json", `{"target":"/shop"}` + strings.Repeat(" ", 4096), 400},
	} {
		t.Run(tt.name, func(t *testing.T) {
			q := &inputQueue{}
			c, e := NewCoordinator(q, model.DefaultConfig())
			if e != nil {
				t.Fatal(e)
			}
			r := httptest.NewRequest("POST", "/_wr/v1/tickets", strings.NewReader(tt.body))
			r.Header.Set("Content-Type", tt.content)
			r.Header.Set("Idempotency-Key", "boundary-fixture-key")
			r.Header.Set("X-WR-Service", c.ServiceKey)
			w := httptest.NewRecorder()
			c.Handler().ServeHTTP(w, r)
			if w.Code != tt.status {
				t.Fatalf("status=%d expected=%d writes=%d", w.Code, tt.status, q.joins)
			}
			want := 0
			if tt.status == 202 {
				want = 1
			}
			if q.joins != want {
				t.Fatal("invalid input reached queue")
			}
		})
	}
}

func TestAmbiguousProtocolHeaders(t *testing.T) {
	for _, header := range []string{"Content-Type", "Idempotency-Key", "X-WR-Service", "Authorization"} {
		t.Run(header, func(t *testing.T) {
			q := &inputQueue{}
			c, e := NewCoordinator(q, model.DefaultConfig())
			if e != nil {
				t.Fatal(e)
			}
			method, path := "POST", "/_wr/v1/tickets"
			if header == "Authorization" {
				method, path = "GET", base+"/status"
			}
			r := httptest.NewRequest(method, path, strings.NewReader(`{"target":"/shop"}`))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Idempotency-Key", "boundary-fixture-key")
			r.Header.Set("X-WR-Service", c.ServiceKey)
			r.Header.Set("Authorization", "Bearer "+base64.RawURLEncoding.EncodeToString(make([]byte, 32)))
			r.Header.Add(header, r.Header.Get(header))
			w := httptest.NewRecorder()
			c.Handler().ServeHTTP(w, r)
			want := 400
			if header == "X-WR-Service" {
				want = 401
			}
			if w.Code != want || q.joins != 0 || q.reads != 0 {
				t.Fatalf("status=%d writes=%d reads=%d", w.Code, q.joins, q.reads)
			}
		})
	}
}

func TestDuplicateAdmissionHeaderRejected(t *testing.T) {
	// The request must stop before signature verification or upstream contact.
	h, e := NewGateway("http://127.0.0.1:1", "http://127.0.0.1:2", "fixture", make([]byte, 32))
	if e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/shop", nil)
	r.Header.Add("X-Waiting-Room-Admission", "first")
	r.Header.Add("X-Waiting-Room-Admission", "second")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal("ambiguous admission accepted", w.Code)
	}
}
