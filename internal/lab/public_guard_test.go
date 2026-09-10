// SPDX-License-Identifier: Apache-2.0
package lab

import (
	"context"
	"encoding/base64"
	"net/http/httptest"
	"strings"
	"testing"
	"waiting-room/internal/publicguard"
	"waiting-room/internal/queue/model"
	"waiting-room/internal/queue/valkeystore"
)

func TestEarlyPollAndGuardFailureNeverReadQueue(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		q := &inputQueue{}
		c, e := NewCoordinator(q, model.DefaultConfig())
		if e != nil {
			t.Fatal(e)
		}
		c.Guard = func(context.Context, string, string, string) (publicguard.Decision, error) {
			if unavailable {
				return publicguard.Decision{}, publicguard.ErrUnavailable
			}
			return publicguard.Decision{RetryAfterMs: 4101}, nil
		}
		r := httptest.NewRequest("GET", base+"/status", nil)
		r.Header.Set("X-WR-Service", c.ServiceKey)
		r.Header.Set("X-WR-Source", strings.Repeat("a", 64))
		r.Header.Set("Authorization", "Bearer "+base64.RawURLEncoding.EncodeToString(make([]byte, 32)))
		w := httptest.NewRecorder()
		c.Handler().ServeHTTP(w, r)
		want := 429
		if unavailable {
			want = 503
		}
		if w.Code != want || q.reads != 0 || q.joins != 0 || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Content-Type") != "application/problem+json" {
			t.Fatal("guard did not fail before queue access", w.Code)
		}
		if !unavailable && w.Header().Get("Retry-After") != "5" {
			t.Fatal("retry rounding")
		}
	}
}
func TestSourceFingerprintIgnoresSpoofedHeadersAndGroupsIPv6(t *testing.T) {
	b := &browserGateway{sourceKey: []byte(strings.Repeat("k", 32))}
	r := httptest.NewRequest("GET", "https://example.test/shop", nil)
	r.RemoteAddr = "[2001:db8:1:2::1]:1200"
	a := b.sourceFingerprint(r)
	r.RemoteAddr = "[2001:db8:1:2::abcd]:4300"
	r.Header.Set("X-WR-Source", strings.Repeat("f", 64))
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	if a != b.sourceFingerprint(r) || len(a) != 64 {
		t.Fatal("untrusted source identity")
	}
	r.RemoteAddr = "[2001:db8:1:3::1]:1234"
	if a == b.sourceFingerprint(r) {
		t.Fatal("independent source collision")
	}
	r.RemoteAddr = "192.0.2.1:1234"
	a = b.sourceFingerprint(r)
	r.RemoteAddr = "[::ffff:192.0.2.1]:4321"
	if a != b.sourceFingerprint(r) {
		t.Fatal("IPv4-mapped identity diverged")
	}
}

func TestPublicMethodMatrixRejectsBeforeQueueOrGuard(t *testing.T) {
	q := &inputQueue{}
	c, e := NewCoordinator(q, model.DefaultConfig())
	if e != nil {
		t.Fatal(e)
	}
	guardCalls := 0
	c.Guard = func(context.Context, string, string, string) (publicguard.Decision, error) {
		guardCalls++
		return publicguard.Decision{Allowed: true}, nil
	}
	for path, allowed := range map[string]string{"/_wr/v1/tickets": "POST", base + "/status": "GET", base + "/admissions": "POST", base + "/heartbeat": "POST"} {
		for _, method := range []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"} {
			if method == allowed {
				continue
			}
			r := httptest.NewRequest(method, path, nil)
			r.Header.Set("X-WR-Service", c.ServiceKey)
			w := httptest.NewRecorder()
			c.Handler().ServeHTTP(w, r)
			if w.Code != 405 || w.Header().Get("Allow") != allowed || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal(method, path, w.Code)
			}
		}
	}
	if guardCalls != 0 || q.joins != 0 || q.reads != 0 {
		t.Fatal("rejected method changed state")
	}
}

type retainedJoinQueue struct {
	inputQueue
	first *valkeystore.Result
}

func (q *retainedJoinQueue) Join(ctx context.Context, key, target, id, replay string) (valkeystore.Result, error) {
	if q.first != nil {
		return *q.first, nil
	}
	out, e := q.inputQueue.Join(ctx, key, target, id, replay)
	q.first = &out
	return out, e
}
func TestEnablingPublicGuardPreservesLegacyJoinResponse(t *testing.T) {
	q := &retainedJoinQueue{}
	c, e := NewCoordinator(q, model.DefaultConfig())
	if e != nil {
		t.Fatal(e)
	}
	join := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/_wr/v1/tickets", strings.NewReader(`{"target":"/shop"}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", strings.Repeat("k", 32))
		r.Header.Set("X-WR-Service", c.ServiceKey)
		r.Header.Set("X-WR-Source", strings.Repeat("a", 64))
		w := httptest.NewRecorder()
		c.Handler().ServeHTTP(w, r)
		return w
	}
	before := join()
	calls := 0
	c.Guard = func(context.Context, string, string, string) (publicguard.Decision, error) {
		calls++
		return publicguard.Decision{Allowed: true, PollAfterMs: 17000}, nil
	}
	after := join()
	if before.Code != 202 || after.Code != 202 || before.Body.String() != after.Body.String() || calls != 2 || q.joins != 1 {
		t.Fatal("guard changed stored join response")
	}
}

func TestConfirmedJoinSurvivesPollRegistrationFailure(t *testing.T) {
	for _, fail := range []string{"unavailable", "throttled"} {
		t.Run(fail, func(t *testing.T) {
			q := &retainedJoinQueue{}
			c, err := NewCoordinator(q, model.DefaultConfig())
			if err != nil {
				t.Fatal(err)
			}
			join := func() *httptest.ResponseRecorder {
				r := httptest.NewRequest("POST", "/_wr/v1/tickets", strings.NewReader(`{"target":"/shop"}`))
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("Idempotency-Key", strings.Repeat("k", 32))
				r.Header.Set("X-WR-Service", c.ServiceKey)
				r.Header.Set("X-WR-Source", strings.Repeat("a", 64))
				w := httptest.NewRecorder()
				c.Handler().ServeHTTP(w, r)
				return w
			}
			registrationFailed := true
			c.Guard = func(_ context.Context, _, op, _ string) (publicguard.Decision, error) {
				if op == "join" || !registrationFailed {
					return publicguard.Decision{Allowed: true}, nil
				}
				if op == "register" && fail == "throttled" {
					return publicguard.Decision{RetryAfterMs: 3000}, nil
				}
				return publicguard.Decision{}, publicguard.ErrUnavailable
			}
			first := join()
			if first.Code != 202 || q.joins != 1 {
				t.Fatalf("confirmed queue write was hidden by optional poll registration: HTTP %d joins %d", first.Code, q.joins)
			}
			// Receiving a retained queue credential grants no admission and cannot
			// bypass unavailable shared status/claim guards.
			for _, op := range []string{"status", "admissions"} {
				method := "GET"
				if op == "admissions" {
					method = "POST"
				}
				r := httptest.NewRequest(method, base+"/"+op, nil)
				r.Header.Set("X-WR-Service", c.ServiceKey)
				r.Header.Set("X-WR-Source", strings.Repeat("a", 64))
				r.Header.Set("Authorization", "Bearer "+base64.RawURLEncoding.EncodeToString(make([]byte, 32)))
				w := httptest.NewRecorder()
				c.Handler().ServeHTTP(w, r)
				if w.Code != 503 || q.reads != 0 {
					t.Fatal("registration fallback bypassed shared guard", op, w.Code)
				}
			}
			registrationFailed = false
			replay := join()
			if replay.Code != 202 || first.Body.String() != replay.Body.String() || q.joins != 1 {
				t.Fatal("registration retry changed confirmed join")
			}
		})
	}
}

func TestUnavailableJoinGuardStillRejectsBeforeQueueWrite(t *testing.T) {
	q := &inputQueue{}
	c, err := NewCoordinator(q, model.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	c.Guard = func(context.Context, string, string, string) (publicguard.Decision, error) {
		return publicguard.Decision{}, publicguard.ErrUnavailable
	}
	r := httptest.NewRequest("POST", "/_wr/v1/tickets", strings.NewReader(`{"target":"/shop"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", strings.Repeat("k", 32))
	r.Header.Set("X-WR-Service", c.ServiceKey)
	r.Header.Set("X-WR-Source", strings.Repeat("a", 64))
	w := httptest.NewRecorder()
	c.Handler().ServeHTTP(w, r)
	if w.Code != 503 || q.joins != 0 {
		t.Fatal("required join quota guard was bypassed", w.Code, q.joins)
	}
}
