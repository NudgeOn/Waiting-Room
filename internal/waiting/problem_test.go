// SPDX-License-Identifier: Apache-2.0
package waiting

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProblemPageSelfContainedAndCredentialFree(t *testing.T) {
	for _, method := range []string{"GET", "HEAD"} {
		r := httptest.NewRequest(method, "https://shop.example/shop?ticket=private-value", nil)
		r.Header.Set("Accept-Language", "en-US")
		r.Header.Set("Authorization", "Bearer private-value")
		w := httptest.NewRecorder()
		w.Header().Set("Retry-After", "59")
		Problem(w, r, 429, "API_RATE_LIMITED")
		if w.Code != 429 || w.Header().Get("Retry-After") != "59" || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("failure contract")
		}
		if method == "HEAD" {
			if w.Body.Len() != 0 {
				t.Fatal("HEAD body")
			}
			continue
		}
		html := w.Body.String()
		if strings.Contains(html, "private-value") || strings.Contains(html, "<script") || !strings.Contains(html, "Please wait a moment") {
			t.Fatal("unsafe failure content")
		}
		start := strings.Index(html, "<style>") + len("<style>")
		end := strings.Index(html, "</style>")
		digest := sha256.Sum256([]byte(html[start:end]))
		if !strings.Contains(w.Header().Get("Content-Security-Policy"), "'sha256-"+base64.StdEncoding.EncodeToString(digest[:])+"'") {
			t.Fatal("styles blocked by CSP")
		}
	}
}
