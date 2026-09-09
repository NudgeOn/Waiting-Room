// SPDX-License-Identifier: Apache-2.0
package lab

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"waiting-room/internal/queue/valkeystore"
)

func TestBrowserCookiesFitMaximumQueryTarget(t *testing.T) {
	b, err := newBrowserGateway("http://127.0.0.1:1", "service", nil, http.DefaultTransport, "calm")
	if err != nil {
		t.Fatal(err)
	}
	target := "/shop?q=" + strings.Repeat("&", 2040)
	if len(target) != 2048 || !b.binding.ValidTarget(target) {
		t.Fatal("invalid maximum fixture")
	}
	r := httptest.NewRequest("GET", "http://127.0.0.1:18080"+target, nil)
	prepareTestBrowser(t, b, r)
	if d, err := b.intent(r); err != nil || d.Target != target {
		t.Fatal("intent target was not preserved", err)
	}
	for _, cookie := range r.Cookies() {
		if len(cookie.String()) > 4096 {
			t.Fatal("oversized intent cookie")
		}
	}
	now := time.Now()
	sealed, err := b.sealReturn(returnData{Host: r.Host, Ticket: valkeystore.Hash("ticket"), Target: target, Issued: now.UnixMilli(), Expires: now.Add(time.Hour).UnixMilli()})
	if err != nil {
		t.Fatal(err)
	}
	if len((&http.Cookie{Name: b.returnCookie(), Value: sealed, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, Expires: now.Add(time.Hour)}).String()) > 4096 {
		t.Fatal("oversized return cookie")
	}
	if d, err := b.openReturn(sealed, r.Host, "ticket", now); err != nil || d.Target != target {
		t.Fatal("return target was not preserved", err)
	}
}

func prepareTestBrowser(t *testing.T, b *browserGateway, request *http.Request) {
	t.Helper()
	for _, confirm := range []bool{false, true} {
		body, _ := browserCookieJSON(map[string]any{"target": request.URL.RequestURI(), "confirm": confirm})
		r := httptest.NewRequest("POST", b.scheme()+"://"+request.Host+b.binding.base()+"/browser-prepare", strings.NewReader(string(body)))
		r.Header.Set("Origin", b.scheme()+"://"+request.Host)
		r.Header.Set("Content-Type", "application/json")
		for _, c := range request.Cookies() {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		b.prepare(w, r)
		if w.Code != 204 {
			t.Fatal("prepare/confirm", confirm, w.Code)
		}
		for _, c := range w.Result().Cookies() {
			request.AddCookie(c)
		}
	}
}

func TestBrowserPrepareCannotAllocateBeforeCookieConfirmation(t *testing.T) {
	b, err := newBrowserGateway("http://127.0.0.1:1", "service", nil, http.DefaultTransport, "calm")
	if err != nil {
		t.Fatal(err)
	}
	initial := httptest.NewRequest("GET", "http://127.0.0.1:18080/shop", nil)
	w := httptest.NewRecorder()
	b.join(w, initial)
	if w.Code != 200 || len(w.Result().Cookies()) != 0 || !strings.Contains(w.Body.String(), "data-bootstrap") {
		t.Fatal("first navigation mutated or did not prepare")
	}
	for _, origin := range []string{"", "https://evil.test", "http://127.0.0.1:18080"} {
		r := httptest.NewRequest("POST", "http://127.0.0.1:18080"+base+"/browser-prepare", strings.NewReader(`{"target":"/shop","confirm":true}`))
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		b.prepare(w, r)
		want := 403
		if origin == "http://127.0.0.1:18080" {
			want = 428
		}
		if w.Code != want || len(w.Result().Cookies()) != 0 {
			t.Fatal("missing cookie or cross-origin accepted", w.Code)
		}
	}
	prepareTestBrowser(t, b, initial)
	d, err := b.intent(initial)
	if err != nil || d.Target != "/shop" {
		t.Fatal("intent missing", err)
	}
	if strings.Contains(initial.Header.Get("Cookie"), d.Nonce) {
		t.Fatal("intent nonce exposed")
	}
	for _, c := range initial.Cookies() {
		if c.Name == b.intentCookie() {
			initial.Header.Set("Cookie", c.Name+"="+c.Value+"x")
		}
	}
	if _, err := b.intent(initial); err == nil {
		t.Fatal("tampered intent accepted")
	}
}

func prepareBrowserClient(t *testing.T, client *http.Client, origin, target string) {
	t.Helper()
	for _, confirm := range []bool{false, true} {
		data, _ := json.Marshal(map[string]any{"target": target, "confirm": confirm})
		r, _ := http.NewRequest("POST", origin+base+"/browser-prepare", strings.NewReader(string(data)))
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", "application/json")
		out, err := client.Do(r)
		if err != nil {
			t.Fatal("browser prepare failed")
		}
		out.Body.Close()
		if out.StatusCode != 204 {
			t.Fatal("browser prepare status", out.StatusCode)
		}
	}
}
