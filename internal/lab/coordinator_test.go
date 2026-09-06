// SPDX-License-Identifier: Apache-2.0
package lab

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"waiting-room/internal/queue/model"
)

func TestTargetValidation(t *testing.T) {
	for _, v := range []string{"/shop", "/shop/item?x=1"} {
		if !ValidTarget(v) {
			t.Fatal(v)
		}
	}
	for _, v := range []string{"//evil.test", "https://evil.test/shop", "/shop/../admin", "/shop/%2e%2e/admin", "/shop/%252e%252e/admin", "/shop/%2fadmin", "/shopping", "/shop/\\evil", "/shop\n", "/shop/%5cadmin"} {
		if ValidTarget(v) {
			t.Fatal(v)
		}
	}
}

func TestHeartbeatIntervalPrecedesConfiguredIdleExpiry(t *testing.T) {
	for _, ttl := range []int64{60000, 120000, 600000} {
		cfg := model.DefaultConfig()
		cfg.IdleTTL = ttl
		c, err := NewCoordinator(nil, cfg)
		if err != nil {
			t.Fatal(err)
		}
		out := c.queued(model.Ticket{JoinedAt: 1000}, "")
		interval := out["heartbeatAfterMs"].(int64)
		if interval != ttl/2 || interval >= ttl {
			t.Fatal("heartbeat outlives idle TTL", ttl, interval)
		}
	}
}
func TestInternalCredentialAndInput(t *testing.T) {
	c, e := NewCoordinator(nil, model.DefaultConfig())
	if e != nil {
		t.Fatal(e)
	}
	for _, input := range []struct {
		key, body string
		expected  int
	}{{"", `{"target":"/shop"}`, 401}, {c.ServiceKey, `{"target":"//evil"}`, 400}, {c.ServiceKey, `{"target":"/shop","unknown":1}`, 400}, {c.ServiceKey, `{"target":"/shop"} {}`, 400}} {
		req := httptest.NewRequest("POST", "/_wr/v1/tickets", strings.NewReader(input.body))
		req.Header.Set("X-WR-Service", input.key)
		req.Header.Set("Idempotency-Key", "1234567890123456")
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		c.Handler().ServeHTTP(w, req)
		if w.Code != input.expected {
			t.Fatal(w.Code, w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("cacheable auth response")
		}
	}
}
func TestLoopbackOnlyAndReservedHeaders(t *testing.T) {
	for _, u := range []string{"https://example.test", "http://localhost:1234", "http://127.0.0.1@evil.test", "http://169.254.169.254"} {
		if _, e := loopbackURL(u); e == nil {
			t.Fatal(u)
		}
	}
	r := httptest.NewRequest("GET", "http://127.0.0.1/shop", nil)
	r.Header.Set("X-WR-Service", "spoof")
	r.Header.Set("X-Waiting-Room-Admission", "token")
	r.Header.Set("Authorization", "Bearer oauth")
	r.AddCookie(&http.Cookie{Name: "wr_dev_queue", Value: "secret"})
	r.AddCookie(&http.Cookie{Name: "session", Value: "customer"})
	strip(r.Header)
	stripCookies(r)
	if r.Header.Get("X-WR-Service") != "" || r.Header.Get("X-Waiting-Room-Admission") != "" || r.Header.Get("Authorization") != "Bearer oauth" || r.Header.Get("Cookie") != "session=customer" {
		t.Fatal("reserved credential leak")
	}
}
