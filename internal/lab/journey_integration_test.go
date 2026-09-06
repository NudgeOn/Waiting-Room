//go:build integration

// SPDX-License-Identifier: Apache-2.0
package lab

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"waiting-room/internal/queue/model"
	"waiting-room/internal/queue/valkeystore"
)

// Real HTTP + Valkey, ten browser-cookie clients and ten app clients. Two
// independent Gateway handlers sit behind one public authority, without affinity.
func TestMixedJourneyIndependentGateways(t *testing.T) {
	addr := os.Getenv("WR_TEST_VALKEY")
	if addr == "" {
		t.Fatal("WR_TEST_VALKEY required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 110*time.Second)
	defer cancel()
	cfg := model.DefaultConfig()
	cfg.LeaseCap = 3
	cfg.Rate = 6
	cfg.AdmissionTTL = 60000
	store, err := valkeystore.Open(ctx, addr, fmt.Sprintf("wr:lab:journey-%d", time.Now().UnixNano()), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	c, err := NewCoordinator(store, cfg)
	if err != nil {
		t.Fatal(err)
	}
	internal := httptest.NewServer(c.Handler())
	defer internal.Close()
	var reached atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Add(1)
		if r.Header.Get("Authorization") != "Bearer customer-oauth" || r.Header.Get("Cookie") != "customer=session" {
			t.Error("customer credentials not preserved")
		}
		for name := range r.Header {
			lower := strings.ToLower(name)
			if strings.HasPrefix(lower, "x-wr-") || strings.HasPrefix(lower, "x-waiting-room-") || strings.HasPrefix(lower, "x-forwarded-") || lower == "forwarded" {
				t.Error("internal/spoofed header reached origin")
			}
		}
		writeJSON(w, 200, map[string]string{"path": r.URL.RequestURI()})
	}))
	defer origin.Close()
	var targets []*url.URL
	returnKey := make([]byte, 32)
	if _, err := rand.Read(returnKey); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		h, err := NewGatewayWithReturnKey(internal.URL, origin.URL, c.ServiceKey, c.Public, "calm", returnKey)
		if err != nil {
			t.Fatal(err)
		}
		gateway := httptest.NewServer(h)
		defer gateway.Close()
		u, _ := url.Parse(gateway.URL)
		targets = append(targets, u)
	}
	var selected atomic.Int32
	var dropClaimResponse atomic.Bool
	front := httptest.NewServer(&httputil.ReverseProxy{Rewrite: func(p *httputil.ProxyRequest) { p.SetURL(targets[selected.Load()]); p.Out.Host = p.In.Host }, ModifyResponse: func(r *http.Response) error {
		if r.Request.Method == "POST" && r.Request.URL.Path == base+"/admissions" && dropClaimResponse.Swap(false) {
			return errors.New("injected loss after upstream claim commit")
		}
		return nil
	}, ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) { problem(w, 503, "QUEUE_UNAVAILABLE") }})
	defer front.Close()
	publicURL, _ := url.Parse(front.URL)
	type visitor struct {
		client                                    *http.Client
		token, target, waiting, sealed, admission string
		browser                                   bool
	}
	visitors := make([]visitor, 20)
	do := func(v *visitor, gw int, method, path string, body []byte, headers map[string]string) (int, []byte, http.Header) {
		t.Helper()
		selected.Store(int32(gw))
		req, _ := http.NewRequestWithContext(ctx, method, front.URL+path, strings.NewReader(string(body)))
		for k, value := range headers {
			req.Header.Set(k, value)
		}
		resp, err := v.client.Do(req)
		if err != nil {
			t.Fatal("HTTP journey request failed (credential-bearing URL omitted)")
		}
		defer resp.Body.Close()
		var data json.RawMessage
		// Waiting HTML and redirects are checked by status; never print credentials.
		if strings.Contains(resp.Header.Get("Content-Type"), "json") {
			_ = json.NewDecoder(resp.Body).Decode(&data)
		}
		return resp.StatusCode, data, resp.Header
	}
	for i := range visitors {
		v := &visitors[i]
		v.browser = i%2 == 0
		v.target = fmt.Sprintf("/shop/visitor-%d?item=%d", i, i)
		v.client = &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		if v.browser {
			v.client.Jar, _ = cookiejar.New(nil)
			code, _, header := do(v, 0, "GET", v.target, nil, map[string]string{"Accept": "text/html"})
			if code != 303 {
				t.Fatal("browser join", i, code)
			}
			v.waiting = header.Get("Location")
			u, _ := url.Parse(v.waiting)
			v.sealed = u.Query().Get("return")
			for _, cookie := range v.client.Jar.Cookies(publicURL) {
				if cookie.Name == queueCookie {
					v.token = cookie.Value
				}
			}
			code, _, _ = do(v, 1, "GET", v.waiting, nil, map[string]string{"Accept": "text/html"})
			if code != 200 {
				t.Fatal("cross-Gateway waiting page rejected", i, code)
			}
		} else {
			payload, _ := json.Marshal(map[string]string{"target": v.target})
			headers := map[string]string{"Content-Type": "application/json", "Idempotency-Key": fmt.Sprintf("journey-visitor-%02d", i)}
			code, data, _ := do(v, 0, "POST", "/_wr/v1/tickets", payload, headers)
			var join struct{ TicketToken string }
			_ = json.Unmarshal(data, &join)
			v.token = join.TicketToken
			if code != 202 || v.token == "" {
				t.Fatal("app join", i, code)
			}
			code, retry, _ := do(v, 1, "POST", "/_wr/v1/tickets", payload, headers)
			if code != 202 || string(data) != string(retry) {
				t.Fatal("app cross-Gateway replay", i)
			}
		}
		state, err := store.Status(ctx, valkeystore.Hash(v.token))
		if err != nil || state.Ticket.Sequence != uint64(i+1) {
			t.Fatal("mixed FIFO join", i, err)
		}
		code, _, _ := do(v, 1, "POST", v.target, []byte("unsafe-body"), nil)
		if code != 429 || reached.Load() != 0 {
			t.Fatal("pre-admission origin bypass", i, code)
		}
	}
	auth := func(v *visitor) map[string]string {
		if v.browser {
			return nil
		}
		return map[string]string{"Authorization": "Bearer " + v.token}
	}
	checkState := func(v *visitor, gw int, want string, wantCode int) {
		t.Helper()
		code, data, _ := do(v, gw, "GET", base+"/status", nil, auth(v))
		var state struct{ State string }
		_ = json.Unmarshal(data, &state)
		if code != wantCode || state.State != want {
			t.Fatal("wrong journey state", want, code)
		}
	}
	for i := range visitors {
		checkState(&visitors[i], i%2, "queued", 202)
	}
	if _, err := store.Promote(ctx, 20); err != nil {
		t.Fatal(err)
	}
	var expiresAt int64
	for i := range visitors {
		v := &visitors[i]
		if i >= 3 {
			checkState(v, i%2, "queued", 202)
			continue
		}
		checkState(v, 1, "ready", 200)
		path := base + "/admissions"
		headers := auth(v)
		if v.browser {
			path += "?return=" + url.QueryEscape(v.sealed)
			headers = map[string]string{"Origin": front.URL}
		}
		dropClaimResponse.Store(true)
		lostCode, _, lostHeader := do(v, 1, "POST", path, nil, headers)
		committed, err := store.Status(ctx, valkeystore.Hash(v.token))
		if lostCode != 503 || lostHeader.Get("Set-Cookie") != "" || err != nil || committed.Ticket.State != model.Admitted {
			t.Fatal("claim loss was not after commit", i, lostCode)
		}
		code, data, h := do(v, 0, "POST", path, nil, headers)
		if v.browser {
			if code != 303 || h.Get("Location") != v.target {
				t.Fatal("browser claim/return", i, code)
			}
			for _, cookie := range v.client.Jar.Cookies(publicURL) {
				if cookie.Name == admissionCookie {
					v.admission = cookie.Value
				}
			}
		} else {
			var claim struct{ AdmissionToken string }
			_ = json.Unmarshal(data, &claim)
			v.admission = claim.AdmissionToken
			if code != 200 {
				t.Fatal("app claim", code)
			}
		}
		if v.admission == "" {
			t.Fatal("missing admission")
		}
		retryCode, retry, retryHeader := do(v, 1, "POST", path, nil, headers)
		if retryCode != code || string(data) != string(retry) || h.Get("Set-Cookie") != retryHeader.Get("Set-Cookie") {
			t.Fatal("claim replay changed", i)
		}
		replayed, err := store.Status(ctx, valkeystore.Hash(v.token))
		if err != nil || *replayed.Ticket != *committed.Ticket {
			t.Fatal("lost-response retry changed admission/expiry", i)
		}
		originHeaders := map[string]string{"Authorization": "Bearer customer-oauth", "X-WR-Service": "spoof", "X-Forwarded-For": "203.0.113.99", "Forwarded": "for=spoof"}
		if v.browser {
			v.client.Jar.SetCookies(publicURL, []*http.Cookie{{Name: "customer", Value: "session", Path: "/"}})
		} else {
			originHeaders["Cookie"] = "customer=session"
			originHeaders["X-Waiting-Room-Admission"] = v.admission
		}
		code, data, _ = do(v, 0, "GET", v.target, nil, originHeaders)
		var got struct{ Path string }
		_ = json.Unmarshal(data, &got)
		if code != 200 || got.Path != v.target {
			t.Fatal("origin target mismatch", i, code)
		}
		state, err := store.Status(ctx, valkeystore.Hash(v.token))
		if err != nil {
			t.Fatal(err)
		}
		expiresAt = max(expiresAt, state.Ticket.AdmissionUntil+cfg.ClockSkew)
	}
	if reached.Load() != 3 {
		t.Fatal("wrong origin count")
	}
	if _, err := store.Promote(ctx, 20); err != nil {
		t.Fatal(err)
	}
	checkState(&visitors[3], 0, "queued", 202)
	t.Log("mixed Quick20: ten browser + ten app, independent Gateways, lost claim responses retried without state changes, FIFO 3 admitted/17 queued, origin reaches exactly 3")
	// Real time, not a forged token or modified store: leases remain reserved
	// through the full admission expiry + 30s verifier leeway.
	waitUntil := func(at time.Time) {
		t.Helper()
		timer := time.NewTimer(time.Until(at))
		defer timer.Stop()
		select {
		case <-ctx.Done():
			t.Fatal("expiry wait deadline")
		case <-timer.C:
		}
	}
	waitUntil(time.UnixMilli(expiresAt - cfg.ClockSkew).Add(20 * time.Millisecond))
	if _, err := store.Promote(ctx, 20); err != nil {
		t.Fatal(err)
	}
	checkState(&visitors[3], 0, "queued", 202)
	t.Log("60-second admission expiry reached: next visitor still queued during 30-second verifier leeway")
	timer := time.NewTimer(time.Until(time.UnixMilli(expiresAt).Add(20 * time.Millisecond)))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		t.Fatal("expiry wait deadline")
	case <-timer.C:
	}
	if _, err := store.Promote(ctx, 20); err != nil {
		t.Fatal(err)
	}
	for i := 3; i < 6; i++ {
		checkState(&visitors[i], i%2, "ready", 200)
	}
	for i := 6; i < 20; i++ {
		checkState(&visitors[i], i%2, "queued", 202)
	}
	// Deliberately resend the expired bearer rather than relying on cookie expiry.
	app := &visitors[1]
	code, _, _ := do(app, 1, "POST", app.target, nil, map[string]string{"X-Waiting-Room-Admission": app.admission})
	if code != 429 || reached.Load() != 3 {
		t.Fatal("expired admission bypass", code)
	}
	internal.Close()
	code, _, _ = do(&visitors[3], 0, "POST", base+"/admissions", nil, auth(&visitors[3]))
	if code != 503 || reached.Load() != 3 {
		t.Fatal("Coordinator loss bypass", code)
	}
	t.Log("real 60s token + 30s leeway elapsed; next FIFO 3 ready; old token blocked; Coordinator loss creates no origin reach")
}
