// SPDX-License-Identifier: Apache-2.0
package lab

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
	"waiting-room/internal/queue/valkeystore"
)

// A ticket expiry uses Valkey's clock. A small positive clock offset must not
// create a return envelope longer than the Gateway's own 24-hour limit.
func TestBrowserReturnWithQueueClockAhead(t *testing.T) {
	coordinator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(absoluteHeader, strconv.FormatInt(time.Now().Add(24*time.Hour+time.Second).UnixMilli(), 10))
		w.WriteHeader(202)
		_ = json.NewEncoder(w).Encode(map[string]string{"ticketToken": strings.Repeat("A", 43)})
	}))
	defer coordinator.Close()
	b, err := newBrowserGateway(coordinator.URL, "service", nil, coordinator.Client().Transport, "calm")
	if err != nil {
		t.Fatal(err)
	}
	join := httptest.NewRequest("GET", "http://127.0.0.1:18080/shop", nil)
	w := httptest.NewRecorder()
	b.join(w, join)
	if w.Code != 303 {
		t.Fatal("join rejected", w.Code)
	}
	page := httptest.NewRequest("GET", "http://127.0.0.1:18080"+w.Header().Get("Location"), nil)
	for _, c := range w.Result().Cookies() {
		page.AddCookie(c)
	}
	w = httptest.NewRecorder()
	b.page(w, page)
	if w.Code != 200 {
		t.Fatal("valid ticket with queue clock ahead rejected", w.Code)
	}
}

func TestSharedReturnKeyIndependentGateways(t *testing.T) {
	key := bytes.Repeat([]byte{42}, 32)
	one, err := newBrowserGatewayWithKey("http://127.0.0.1:1", "service", nil, http.DefaultTransport, "calm", key)
	if err != nil {
		t.Fatal(err)
	}
	two, err := newBrowserGatewayWithKey("http://127.0.0.1:1", "service", nil, http.DefaultTransport, "calm", key)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	d := returnData{Host: "127.0.0.1:18080", Ticket: valkeystore.Hash("ticket"), Target: "/shop/item?q=1", Issued: now.UnixMilli(), Expires: now.Add(time.Hour).UnixMilli()}
	sealed, err := one.sealReturn(d)
	if err != nil {
		t.Fatal(err)
	}
	// Caller mutation must not change an already constructed Gateway key.
	key[0]++
	if got, err := two.openReturn(sealed, d.Host, "ticket", now); err != nil || got != d {
		t.Fatal("cross-instance return failed")
	}
	if _, err := two.openReturn(sealed, "127.0.0.1:18081", "ticket", now); err == nil {
		t.Fatal("host binding bypassed")
	}
	wrong, err := newBrowserGatewayWithKey("http://127.0.0.1:1", "service", nil, http.DefaultTransport, "calm", key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wrong.openReturn(sealed, d.Host, "ticket", now); err == nil {
		t.Fatal("wrong key accepted")
	}
	for _, bad := range [][]byte{nil, make([]byte, 32), bytes.Repeat([]byte{1}, 16), bytes.Repeat([]byte{1}, 31), bytes.Repeat([]byte{1}, 33)} {
		if _, err := newBrowserGatewayWithKey("http://127.0.0.1:1", "service", nil, http.DefaultTransport, "calm", bad); err == nil {
			t.Fatal("invalid key accepted")
		}
	}
}

func TestReturnSealingBindings(t *testing.T) {
	b, err := newBrowserGateway("http://127.0.0.1:1", "service", nil, http.DefaultTransport, "calm")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	d := returnData{Host: "127.0.0.1:18080", Ticket: valkeystore.Hash("ticket"), Target: "/shop/item?q=1", Issued: now.UnixMilli(), Expires: now.Add(time.Hour).UnixMilli()}
	sealed, _ := b.sealReturn(d)
	if strings.Contains(sealed, d.Target) {
		t.Fatal("target visible")
	}
	if got, err := b.openReturn(sealed, d.Host, "ticket", now); err != nil || got != d {
		t.Fatal("round trip", err)
	}
	for _, input := range []struct {
		value, host, ticket string
		at                  time.Time
	}{
		{sealed + "x", d.Host, "ticket", now}, {sealed, "127.0.0.1:18081", "ticket", now}, {sealed, d.Host, "other", now},
		{sealed, d.Host, "ticket", now.Add(time.Hour)}, {sealed, d.Host, "ticket", now.Add(-time.Millisecond)},
		{strings.Repeat("x", 4097), d.Host, "ticket", now},
	} {
		if _, err := b.openReturn(input.value, input.host, input.ticket, input.at); err == nil {
			t.Fatal("invalid binding accepted")
		}
	}
	other, _ := newBrowserGateway("http://127.0.0.1:1", "service", nil, http.DefaultTransport, "calm")
	if _, err := other.openReturn(sealed, d.Host, "ticket", now); err == nil {
		t.Fatal("wrong key accepted")
	}
	d.Target = "//evil.test"
	bad, _ := b.sealReturn(d)
	if _, err := b.openReturn(bad, d.Host, "ticket", now); err == nil {
		t.Fatal("unsafe target accepted")
	}
}
func TestLabCookieAndHostBoundary(t *testing.T) {
	w := httptest.NewRecorder()
	setLabCookie(w, queueCookie, "ticket", time.Now().Add(time.Hour))
	c := w.Result().Cookies()[0]
	if !c.HttpOnly || c.Secure || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.Domain != "" {
		t.Fatal("lab cookie flags")
	}
	for _, host := range []string{"evil.test:18080", "localhost:18080", "127.0.0.1", "127.0.0.1:99999", "127.0.0.1:0"} {
		r := httptest.NewRequest("GET", "/shop", nil)
		r.Host = host
		if browserHost(r) {
			t.Fatal("invalid host accepted", host)
		}
	}
	r := httptest.NewRequest("GET", "http://127.0.0.1:18080/shop", nil)
	if !browserHost(r) {
		t.Fatal("local host rejected")
	}
	r.AddCookie(&http.Cookie{Name: queueCookie, Value: "one"})
	r.AddCookie(&http.Cookie{Name: queueCookie, Value: "two"})
	if _, err := uniqueCookie(r, queueCookie); err == nil {
		t.Fatal("ambiguous cookies accepted")
	}
}
func TestCookieMutationCSRFBeforeCoordinator(t *testing.T) {
	b, _ := newBrowserGateway("http://127.0.0.1:1", "service", nil, http.DefaultTransport, "calm")
	for _, input := range []struct{ origin, csrf, auth string }{
		{"", "", ""}, {"https://evil.test", "bogus", ""}, {"http://127.0.0.1:18080", "bogus", ""}, {"http://127.0.0.1:18080", "", "Bearer app"},
	} {
		r := httptest.NewRequest("POST", "http://127.0.0.1:18080"+base+"/heartbeat", nil)
		r.AddCookie(&http.Cookie{Name: queueCookie, Value: "ticket"})
		r.Header.Set("Origin", input.origin)
		r.Header.Set("X-Waiting-Room-CSRF", input.csrf)
		r.Header.Set("Authorization", input.auth)
		w := httptest.NewRecorder()
		if !b.cookieAPI(w, r) || (w.Code != 400 && w.Code != 403) {
			t.Fatal("CSRF/ambiguity not blocked before backend", w.Code)
		}
	}
}

func TestBrowserResumeDoesNotConsumePollOrExtendTicket(t *testing.T) {
	calls := 0
	coordinator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls > 1 {
			problem(w, 429, "API_RATE_LIMITED")
			return
		}
		w.Header().Set(absoluteHeader, strconv.FormatInt(time.Now().Add(time.Hour).UnixMilli(), 10))
		w.WriteHeader(202)
		_ = json.NewEncoder(w).Encode(map[string]string{"ticketToken": strings.Repeat("A", 43)})
	}))
	defer coordinator.Close()
	b, e := newBrowserGateway(coordinator.URL, "service", nil, coordinator.Client().Transport, "calm")
	if e != nil {
		t.Fatal(e)
	}
	first := httptest.NewRecorder()
	b.join(first, httptest.NewRequest("GET", "http://127.0.0.1:18080/shop/a", nil))
	if first.Code != 303 {
		t.Fatal(first.Code)
	}
	req := httptest.NewRequest("GET", "http://127.0.0.1:18080/shop/b?tab=2", nil)
	var ticket, sealed string
	for _, c := range first.Result().Cookies() {
		req.AddCookie(c)
		if c.Name == b.queueCookie() {
			ticket = c.Value
		}
		if c.Name == b.returnCookie() {
			sealed = c.Value
		}
	}
	before, e := b.openReturn(sealed, req.Host, ticket, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	next := httptest.NewRecorder()
	b.join(next, req)
	if next.Code != 303 || calls != 1 {
		t.Fatal("resume consumed a poll", next.Code, calls)
	}
	for _, c := range next.Result().Cookies() {
		if c.Name == b.queueCookie() {
			t.Fatal("resume extended ticket cookie")
		}
		if c.Name == b.returnCookie() {
			d, e := b.openReturn(c.Value, req.Host, ticket, time.Now())
			if e != nil || d.Expires != before.Expires || d.Target != req.URL.RequestURI() {
				t.Fatal("resume changed expiry or target", e)
			}
		}
	}
	// The original tab's return remains independently valid after a second tab.
	if _, e := b.openReturn(sealed, req.Host, ticket, time.Now()); e != nil {
		t.Fatal("old tab invalidated")
	}
	bad := httptest.NewRequest("GET", "http://127.0.0.1:18080/shop", nil)
	bad.AddCookie(&http.Cookie{Name: b.queueCookie(), Value: ticket})
	bad.AddCookie(&http.Cookie{Name: b.returnCookie(), Value: sealed + "x"})
	out := httptest.NewRecorder()
	b.join(out, bad)
	if out.Code != 429 || calls != 2 {
		t.Fatal("tampered resume bypassed queue status", out.Code, calls)
	}
}
