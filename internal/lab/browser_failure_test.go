// SPDX-License-Identifier: Apache-2.0
package lab

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type failedBrowserTransport struct{}

func (failedBrowserTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("unreachable fixture coordinator")
}

func TestBrowserNavigationTransportFailure(t *testing.T) {
	for _, existing := range []bool{false, true} {
		for _, method := range []string{"GET", "HEAD"} {
			for _, html := range []bool{false, true} {
				b, err := newBrowserGateway("http://127.0.0.1:1", "service", nil, failedBrowserTransport{}, "calm")
				if err != nil {
					t.Fatal(err)
				}
				r := httptest.NewRequest(method, "http://127.0.0.1:18080/shop?private=hidden", nil)
				if html {
					r.Header.Set("Accept", "text/html")
				}
				if existing {
					r.AddCookie(&http.Cookie{Name: b.queueCookie(), Value: "private-ticket"})
				}
				prepareTestBrowser(t, b, r)
				w := httptest.NewRecorder()
				b.join(w, r)
				if w.Code != 503 || w.Header().Get("Cache-Control") != "no-store" {
					t.Fatalf("failure contract: %d", w.Code)
				}
				if html {
					if !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
						t.Errorf("existing=%v method=%s: navigation failure lacks HTML retry page", existing, method)
					}
					if method == "GET" && !strings.Contains(w.Body.String(), "다시 확인하기") {
						t.Error("missing retry link")
					}
					if method == "HEAD" && w.Body.Len() != 0 {
						t.Error("HEAD response has body")
					}
				} else if !strings.Contains(w.Header().Get("Content-Type"), "json") {
					t.Error("app failure must remain JSON")
				}
				for _, secret := range []string{"private-ticket", "private=hidden", "unreachable fixture coordinator"} {
					if strings.Contains(w.Body.String(), secret) {
						t.Error("failure page leaked request or transport details")
					}
				}
			}
		}
	}
}
