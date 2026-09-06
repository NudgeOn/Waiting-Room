// SPDX-License-Identifier: Apache-2.0
package lab

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"waiting-room/internal/queue/valkeystore"
)

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
