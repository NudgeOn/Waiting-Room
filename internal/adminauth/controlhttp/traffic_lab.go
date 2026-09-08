// SPDX-License-Identifier: Apache-2.0
package controlhttp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"waiting-room/internal/adminauth"
	"waiting-room/internal/adminauth/pgstore"
)

type TrafficBackend interface {
	List(context.Context, string, string) (pgstore.ControlReply, error)
	Start(context.Context, string, *http.Request, []byte) (pgstore.ControlReply, error)
	Cancel(context.Context, string, string, *http.Request, []byte) (pgstore.ControlReply, error)
}
type TrafficHandler struct{ backend TrafficBackend }

func NewTraffic(b TrafficBackend) *TrafficHandler { return &TrafficHandler{b} }

var labRoute = regexp.MustCompile(`^/api/admin/v1/lab/runs/([A-Za-z0-9_-]{43})(/cancel)?$`)

func TrafficPath(p string) bool {
	return p == "/api/admin/v1/lab/runs" || strings.HasPrefix(p, "/api/admin/v1/lab/runs/")
}
func (h *TrafficHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.URL.EscapedPath() != r.URL.Path || r.URL.RawQuery != "" || r.URL.ForceQuery {
		problem(w, 400, "INVALID_REQUEST")
		return
	}
	id, cancel := "", false
	if r.URL.Path != "/api/admin/v1/lab/runs" {
		m := labRoute.FindStringSubmatch(r.URL.Path)
		if m == nil {
			problem(w, 404, "NOT_FOUND")
			return
		}
		id, cancel = m[1], m[2] != ""
	}
	allowed := "GET"
	if id == "" {
		allowed = "GET, POST"
	}
	if cancel {
		allowed = "POST"
	}
	if !strings.Contains(allowed, r.Method) || (r.Method != "GET" && r.Method != "POST") {
		w.Header().Set("Allow", allowed)
		problem(w, 405, "INVALID_REQUEST")
		return
	}
	if len(r.Header.Values("Authorization")) != 0 || len(r.Header.Values("X-Bootstrap-Token")) != 0 {
		problem(w, 401, "UNAUTHENTICATED")
		return
	}
	token, count := "", 0
	for _, cookie := range r.Cookies() {
		if cookie.Name == "__Host-wrs" {
			token = cookie.Value
			count++
		}
	}
	if count != 1 || len(token) != 43 {
		problem(w, 401, "UNAUTHENTICATED")
		return
	}
	var raw []byte
	var err error
	if r.Body != nil {
		raw, err = io.ReadAll(io.LimitReader(r.Body, 1025))
	}
	if err != nil || len(raw) > 1024 || (r.Method == "GET" && len(raw) > 0) || len(r.Header.Values("Content-Encoding")) > 0 {
		problem(w, 400, "INVALID_REQUEST")
		return
	}
	if r.Method == "POST" && (len(r.Header.Values("Content-Type")) != 1 || r.Header.Get("Content-Type") != "application/json") {
		problem(w, 415, "INVALID_REQUEST")
		return
	}
	var out pgstore.ControlReply
	if r.Method == "GET" {
		out, err = h.backend.List(r.Context(), token, id)
	} else if cancel {
		out, err = h.backend.Cancel(r.Context(), token, id, r, raw)
	} else {
		out, err = h.backend.Start(r.Context(), token, r, raw)
	}
	if err != nil {
		switch {
		case errors.Is(err, adminauth.ErrUnauthenticated):
			problem(w, 401, "UNAUTHENTICATED")
		case errors.Is(err, adminauth.ErrForbidden):
			problem(w, 403, "FORBIDDEN")
		default:
			problem(w, 503, "AUTH_UNAVAILABLE")
		}
		return
	}
	if out.Replay {
		w.Header().Set("Idempotency-Replayed", "true")
	}
	w.Header().Set("Content-Type", "application/json")
	if out.Status >= 400 {
		w.Header().Set("Content-Type", "application/problem+json")
	}
	w.WriteHeader(out.Status)
	_, _ = w.Write(out.Body)
}
