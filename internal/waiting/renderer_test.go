// SPDX-License-Identifier: Apache-2.0
package waiting

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTemplateRegistry(t *testing.T) {
	entries := AvailableTemplates()
	if len(entries) != 1 || entries[0].ID != "calm" {
		t.Fatal(entries)
	}
	entries[0].ID = "changed"
	if AvailableTemplates()[0].ID != "calm" {
		t.Fatal("mutable registry")
	}
	for _, id := range []string{"", "../calm", "CALM", "https://evil.test/theme"} {
		if _, err := NewRenderer(id); err == nil {
			t.Fatal("accepted unknown template", id)
		}
	}
}
func TestTemplateEscapingAndCommonControls(t *testing.T) {
	r, err := NewRenderer("calm")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err = r.Render(&out, Page{Target: `/shop?q="<script>`, Return: `" onload="alert(1)`, ClaimURL: "/_wr/v1/claim"})
	if err != nil {
		t.Fatal(err)
	}
	html := out.String()
	for _, required := range []string{`data-template="calm"`, `/_wr/assets/calm.css`, `/_wr/assets/waiting.js`, `id="claim"`, `id="language"`, `aria-live="polite"`, "예상 대기 시간은 아직 계산 중이에요."} {
		if !strings.Contains(html, required) {
			t.Fatal("missing common contract", required)
		}
	}
	if strings.Contains(html, `<script>`) || strings.Contains(html, `data-return="" onload=`) {
		t.Fatal("unescaped user data")
	}
}
func TestAssetAllowlist(t *testing.T) {
	for _, input := range []struct {
		method, path string
		want         int
	}{
		{"GET", "/_wr/assets/calm.css", 200}, {"GET", "/_wr/assets/waiting.js", 200}, {"HEAD", "/_wr/assets/calm.css", 200},
		{"POST", "/_wr/assets/calm.css", 405}, {"GET", "/_wr/assets/../page.html", 404}, {"GET", "/_wr/assets/custom.css", 404},
	} {
		w := httptest.NewRecorder()
		Asset(w, httptest.NewRequest(input.method, input.path, nil))
		if w.Code != input.want {
			t.Fatal(input, w.Code)
		}
		if input.method == "HEAD" && w.Body.Len() != 0 {
			t.Fatal("HEAD body")
		}
		if w.Code == 200 && (w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff") {
			t.Fatal("asset headers")
		}
	}
}
