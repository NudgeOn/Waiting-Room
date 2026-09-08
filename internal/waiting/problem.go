// SPDX-License-Identifier: Apache-2.0
package waiting

import (
	"crypto/sha256"
	"encoding/base64"
	"html/template"
	"net/http"
	"strings"
)

// Problem is a self-contained failure page. It works even when configuration
// and queue storage are unavailable, and never automatically retries a join.
func Problem(w http.ResponseWriter, r *http.Request, status int, code string) {
	title, message, retry := "잠시 입장을 멈췄어요", "새 입장을 잠시 받을 수 없어요. 잠시 후 다시 확인해 주세요.", "다시 확인하기"
	lang := "ko"
	if strings.HasPrefix(strings.ToLower(r.Header.Get("Accept-Language")), "en") {
		lang, title, message, retry = "en", "Entry is temporarily paused", "We cannot accept new entries right now. Please check again shortly.", "Check again"
	}
	if code == "API_RATE_LIMITED" {
		if lang == "en" {
			title, message = "Please wait a moment", "Requests are arriving too quickly. Please wait before checking again."
		} else {
			title, message = "잠시 후 다시 확인해 주세요", "요청이 빠르게 들어오고 있어요. 잠시 기다린 뒤 다시 확인해 주세요."
		}
	}
	css, _ := files.ReadFile("templates/calm.css")
	digest := sha256.Sum256(css)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if w.Header().Get("Retry-After") == "" {
		w.Header().Set("Retry-After", "3")
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src data:; style-src 'sha256-"+base64.StdEncoding.EncodeToString(digest[:])+"'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	w.WriteHeader(status)
	if r.Method == "HEAD" {
		return
	}
	// Only compiled CSS is trusted. No request URL, header or credential is rendered.
	_ = problemPage.Execute(w, struct {
		Lang, Title, Message, Retry string
		CSS                         template.CSS
	}{lang, title, message, retry, template.CSS(css)})
}

var problemPage = template.Must(template.New("problem").Parse(`<!doctype html>
<html lang="{{.Lang}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Waiting Room</title><link rel="icon" href="data:,"><style>{{.CSS}}</style></head>
<body data-state="unavailable"><header class="site-header"><span class="wordmark">Waiting Room</span></header>
<main><section class="intro" aria-labelledby="heading"><h1 id="heading">{{.Title}}</h1><p class="description">{{.Message}}</p></section><a class="primary" href="">{{.Retry}}</a></main><footer>Powered by Waiting Room</footer></body></html>`))
