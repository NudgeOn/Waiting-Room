//go:build integration

// SPDX-License-Identifier: Apache-2.0
package lab

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"waiting-room/internal/queue/model"
	"waiting-room/internal/queue/valkeystore"
)

func TestBrowserRefreshTabsClaimAndFailure(t *testing.T) {
	addr := os.Getenv("WR_TEST_VALKEY")
	if addr == "" {
		t.Fatal("WR_TEST_VALKEY required")
	}
	ctx := context.Background()
	store, err := valkeystore.Open(ctx, addr, fmt.Sprintf("wr:lab:browser-%d", time.Now().UnixNano()), model.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	c, err := NewCoordinator(store, model.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	internal := httptest.NewServer(c.Handler())
	defer internal.Close()
	var reached atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Add(1)
		if strings.Contains(r.Header.Get("Cookie"), "wr_dev_") || r.Header.Get("X-Waiting-Room-CSRF") != "" || r.Header.Get("X-WR-Service") != "" {
			t.Error("origin credential leak")
		}
		if r.Header.Get("Authorization") != "Bearer customer-oauth" || r.Header.Get("Cookie") != "session=customer" {
			t.Error("customer credentials lost")
		}
		w.WriteHeader(200)
	}))
	defer origin.Close()
	h, err := NewGateway(internal.URL, origin.URL, c.ServiceKey, c.Public)
	if err != nil {
		t.Fatal(err)
	}
	gateway := httptest.NewServer(h)
	defer gateway.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	do := func(method, path string, headers map[string]string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(method, gateway.URL+path, nil)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}
	nav := map[string]string{"Accept": "text/html"}
	prepareBrowserClient(t, client, gateway.URL, "/shop/first?item=one")
	first := do("GET", "/shop/first?item=one", nav)
	if first.StatusCode != 303 {
		t.Fatal("no browser join", first.StatusCode)
	}
	firstURL := first.Header.Get("Location")
	u, _ := url.Parse(gateway.URL)
	var ticket string
	for _, c := range jar.Cookies(u) {
		if c.Name == queueCookie {
			ticket = c.Value
		}
	}
	before, err := store.Status(ctx, valkeystore.Hash(ticket))
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		page := do("GET", firstURL, nav)
		data, _ := io.ReadAll(page.Body)
		if page.StatusCode != 200 || !strings.Contains(string(data), `data-template="calm"`) || strings.Contains(string(data), ticket) || page.Header.Get("Content-Security-Policy") == "" {
			t.Fatal("invalid waiting page")
		}
	}
	second := do("GET", "/shop/second?item=two", nav)
	secondURL := second.Header.Get("Location")
	after, err := store.Status(ctx, valkeystore.Hash(ticket))
	if err != nil || *before.Ticket != *after.Ticket || before.Ticket.Sequence != 1 {
		t.Fatal("refresh/tabs changed ticket")
	}
	if firstURL == secondURL || second.StatusCode != 303 {
		t.Fatal("tab target not independent")
	}
	status := do("GET", base+"/status", nil)
	if status.StatusCode != 202 || status.Header.Get(absoluteHeader) != "" {
		t.Fatal("public status contract")
	}
	bad := do("GET", firstURL+"x", nav)
	if bad.StatusCode != 400 {
		t.Fatal("tampered return accepted")
	}
	parsed, _ := url.Parse(firstURL)
	sealed := parsed.Query().Get("return")
	csrf := do("POST", base+"/heartbeat", map[string]string{"Origin": "https://evil.test", "X-Waiting-Room-CSRF": sealed})
	if csrf.StatusCode != 403 {
		t.Fatal("cross-origin mutation accepted")
	}
	beat := do("POST", base+"/heartbeat", map[string]string{"Origin": gateway.URL, "X-Waiting-Room-CSRF": sealed})
	if beat.StatusCode != 204 {
		t.Fatal("valid heartbeat rejected", beat.StatusCode)
	}
	if _, err := store.Promote(ctx, 1); err != nil {
		t.Fatal(err)
	}
	claimPath := base + "/admissions?return=" + url.QueryEscape(sealed)
	claim := do("POST", claimPath, map[string]string{"Origin": gateway.URL})
	if claim.StatusCode != 303 || claim.Header.Get("Location") != "/shop/first?item=one" {
		t.Fatal("claim redirect", claim.StatusCode)
	}
	retry := do("POST", claimPath, map[string]string{"Origin": gateway.URL})
	if retry.Header.Get("Set-Cookie") != claim.Header.Get("Set-Cookie") {
		t.Fatal("claim replay changed")
	}
	jar.SetCookies(u, []*http.Cookie{{Name: "session", Value: "customer", Path: "/"}})
	if do("GET", "/shop/first?item=one", map[string]string{"Authorization": "Bearer customer-oauth"}).StatusCode != 200 || reached.Load() != 1 {
		t.Fatal("origin not reached")
	}
	// Remove only admission cookie, retain queue credential, then lose Coordinator.
	jar.SetCookies(u, []*http.Cookie{{Name: admissionCookie, Value: "", Path: "/", MaxAge: -1}})
	internal.Close()
	// The authenticated return envelope can still open a waiting page without
	// queue access. Its status request remains fail-closed during the outage.
	resumed := do("GET", "/shop", nav)
	if resumed.StatusCode != 303 || do("GET", resumed.Header.Get("Location"), nav).StatusCode != 200 || do("GET", base+"/status", nil).StatusCode != 503 || reached.Load() != 1 {
		t.Fatal("resume bypassed unavailable Coordinator")
	}
	jar.SetCookies(u, []*http.Cookie{{Name: "wr_dev_r_" + Room, Value: "", Path: "/", MaxAge: -1}})
	if do("GET", "/shop", nav).StatusCode != 503 {
		t.Fatal("unknown status rejoined")
	}
	if do("POST", "/shop", nav).StatusCode != 429 || reached.Load() != 1 {
		t.Fatal("unsafe bypass")
	}
	t.Log("real Valkey: refresh/tabs stable; cookie/CSP; CSRF; deterministic claim; relative return; no origin credential leak; unavailable fail-closed")
}
