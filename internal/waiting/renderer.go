// SPDX-License-Identifier: Apache-2.0
// Package waiting supplies trusted built-in presentation templates, never queue policy.
package waiting

import (
	"embed"
	"fmt"
	"html/template"
	"io"
	"net/http"
)

//go:embed page.html waiting.js join.js templates/*.css
var files embed.FS

type TemplateInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

var registry = []TemplateInfo{{ID: "calm", Name: "Calm"}}

// AvailableTemplates returns an independent copy for CLI and future Backoffice consumers.
func AvailableTemplates() []TemplateInfo { return append([]TemplateInfo(nil), registry...) }

type Renderer struct {
	id   string
	page *template.Template
}

func NewRenderer(id string) (*Renderer, error) {
	for _, entry := range registry {
		if entry.ID == id {
			page, err := template.ParseFS(files, "page.html")
			return &Renderer{id: id, page: page}, err
		}
	}
	return nil, fmt.Errorf("unknown waiting-page template %q", id)
}

type Page struct {
	Room, PrepareURL                                         string
	StatusURL                                                string
	HeartbeatURL                                             string
	ClaimURL                                                 string
	Return                                                   string
	Target                                                   string
	ThemeTitle, ThemeMessage, ThemeLocale, ThemeURL, LogoURL string
	ThemeEnabled, ShowEstimatedWait                          bool
}

func (r *Renderer) Render(w io.Writer, page Page) error {
	return r.page.Execute(w, struct {
		Page
		TemplateID string
	}{page, r.id})
}

// Asset serves only registered, compiled assets. It is not a filesystem server.
func Asset(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		w.WriteHeader(405)
		return
	}
	path, mime := "", ""
	if req.URL.Path == "/_wr/assets/join.js" {
		path, mime = "join.js", "text/javascript; charset=utf-8"
	}
	if req.URL.Path == "/_wr/assets/waiting.js" {
		path, mime = "waiting.js", "text/javascript; charset=utf-8"
	}
	for _, entry := range registry {
		if req.URL.Path == "/_wr/assets/"+entry.ID+".css" {
			path, mime = "templates/"+entry.ID+".css", "text/css; charset=utf-8"
		}
	}
	if path == "" {
		http.NotFound(w, req)
		return
	}
	data, err := files.ReadFile(path)
	if err != nil {
		http.Error(w, "asset unavailable", 503)
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if req.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}
