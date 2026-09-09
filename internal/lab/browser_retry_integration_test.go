//go:build integration

// SPDX-License-Identifier: Apache-2.0
package lab

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"waiting-room/internal/queue/model"
	"waiting-room/internal/queue/valkeystore"
)

func TestBrowserLostInitialResponseAndExpiredRejoin(t *testing.T) {
	ctx := context.Background()
	address := os.Getenv("WR_TEST_VALKEY")
	if address != "127.0.0.1:16379" {
		t.Fatal("dedicated Valkey required")
	}
	cfg := model.DefaultConfig()
	cfg.IdleTTL = 2000
	q, err := valkeystore.Open(ctx, address, fmt.Sprintf("wr:lab:browser-retry-%d", time.Now().UnixNano()), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	c, err := NewCoordinator(q, cfg)
	if err != nil {
		t.Fatal(err)
	}
	internal := httptest.NewServer(c.Handler())
	defer internal.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("unadmitted origin request") }))
	defer origin.Close()
	key := make([]byte, 32)
	rand.Read(key)
	var targets []*url.URL
	for range 2 {
		h, err := NewGatewayWithReturnKey(internal.URL, origin.URL, c.ServiceKey, c.Public, "calm", key)
		if err != nil {
			t.Fatal(err)
		}
		s := httptest.NewServer(h)
		defer s.Close()
		u, _ := url.Parse(s.URL)
		targets = append(targets, u)
	}
	var round atomic.Uint32
	var drop atomic.Bool
	drop.Store(true)
	var original string
	front := httptest.NewServer(&httputil.ReverseProxy{Rewrite: func(p *httputil.ProxyRequest) { p.SetURL(targets[round.Add(1)%2]); p.Out.Host = p.In.Host }, ModifyResponse: func(r *http.Response) error {
		if r.StatusCode == 303 && strings.HasPrefix(r.Request.URL.Path, "/shop") && drop.Swap(false) {
			for _, cookie := range r.Cookies() {
				if cookie.Name == queueCookie {
					original = cookie.Value
				}
			}
			return errors.New("injected loss after committed first join")
		}
		return nil
	}, ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) { problem(w, 503, "QUEUE_UNAVAILABLE") }})
	defer front.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	prepareBrowserClient(t, client, front.URL, "/shop/original")
	get := func(path string) int {
		r, _ := http.NewRequest("GET", front.URL+path, nil)
		r.Header.Set("Accept", "text/html")
		out, err := client.Do(r)
		if err != nil {
			return 0
		}
		out.Body.Close()
		return out.StatusCode
	}
	if get("/shop/original") != 503 || original == "" {
		t.Fatal("first reply not lost")
	}
	u, _ := url.Parse(front.URL)
	for _, cookie := range jar.Cookies(u) {
		if cookie.Name == queueCookie {
			t.Fatal("lost response installed queue credential")
		}
	}
	before, err := q.Status(ctx, valkeystore.Hash(original))
	if err != nil || before.Ticket.Sequence != 1 {
		t.Fatal("initial queue missing", err)
	}
	// Concurrent callers already possess the confirmed, shared intent. The
	// server must return one credential even before any queue cookie arrives.
	var wg sync.WaitGroup
	failures := make(chan int, 12)
	for i := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if code := get(fmt.Sprintf("/shop/tab-%d", i)); code != 303 {
				failures <- code
			}
		}()
	}
	wg.Wait()
	close(failures)
	for code := range failures {
		t.Fatal("retry failed", code)
	}
	var current string
	for _, cookie := range jar.Cookies(u) {
		if cookie.Name == queueCookie {
			current = cookie.Value
		}
	}
	if current != original {
		t.Fatal("retry changed queue identity")
	}
	after, err := q.Status(ctx, valkeystore.Hash(current))
	if err != nil || *before.Ticket != *after.Ticket {
		t.Fatal("retry mutated original ticket", err)
	}
	// Wait on the original real server deadline, then explicitly prepare a
	// replacement. A valid return envelope must not trap the visitor forever.
	time.Sleep(max(0, time.Until(time.UnixMilli(before.Ticket.IdleUntil))) + 50*time.Millisecond)
	for _, confirm := range []string{"false", "true"} {
		r, _ := http.NewRequest("POST", front.URL+base+"/browser-prepare", strings.NewReader(`{"target":"/shop/rejoin","restart":true,"confirm":`+confirm+`}`))
		r.Header.Set("Origin", front.URL)
		r.Header.Set("Content-Type", "application/json")
		out, err := client.Do(r)
		if err != nil {
			t.Fatal("rejoin preparation failed")
		}
		out.Body.Close()
		if out.StatusCode != 204 {
			t.Fatal("rejoin preparation", out.StatusCode)
		}
	}
	if get("/shop/rejoin") != 303 {
		t.Fatal("expired visitor did not rejoin")
	}
	for _, cookie := range jar.Cookies(u) {
		if cookie.Name == queueCookie {
			current = cookie.Value
		}
	}
	if current == original {
		t.Fatal("expired ticket replay loop")
	}
	newTicket, err := q.Status(ctx, valkeystore.Hash(current))
	if err != nil || newTicket.Ticket.Sequence != 2 {
		t.Fatal("duplicate initial joins or missing rejoin", err)
	}
}
