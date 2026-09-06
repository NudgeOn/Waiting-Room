// SPDX-License-Identifier: Apache-2.0
package controlhttp

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"waiting-room/internal/adminauth"
	"waiting-room/internal/adminauth/pgstore"
	"waiting-room/internal/control"
)

type PublicationBackend interface {
	Publish(context.Context, string, *http.Request, []byte) (pgstore.ControlReply, error)
	View(context.Context, string) (pgstore.ControlReply, error)
	Runtime(context.Context, string, string) (pgstore.ControlReply, error)
	Operate(context.Context, string, string, *http.Request, []byte) (pgstore.ControlReply, error)
	Events(context.Context, string, string) (pgstore.ControlReply, error)
	ChangeEvent(context.Context, string, string, string, *http.Request, []byte) (pgstore.ControlReply, error)
}
type RuntimeHandler struct{ backend PublicationBackend }

func NewRuntime(b PublicationBackend) (*RuntimeHandler, error) {
	if b == nil {
		return nil, adminauth.ErrAuthUnavailable
	}
	return &RuntimeHandler{b}, nil
}

var roomRoute = regexp.MustCompile(`^/api/admin/v1/rooms/([a-z][a-z0-9_-]{0,63})/(runtime|events)$`)
var eventRoute = regexp.MustCompile(`^/api/admin/v1/events/([A-Za-z0-9_-]{1,80})(/resume)?$`)

func RuntimePath(p string) bool {
	return p == "/api/admin/v1/config/publish" || p == "/api/admin/v1/config/delivery" || roomRoute.MatchString(p) || eventRoute.MatchString(p)
}
func (h *RuntimeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.URL.EscapedPath() != r.URL.Path || r.URL.RawQuery != "" || r.URL.ForceQuery {
		problem(w, 400, "INVALID_REQUEST")
		return
	}
	p := r.URL.Path
	room, event := "", ""
	kind := ""
	allowed := ""
	switch p {
	case "/api/admin/v1/config/publish":
		kind = "publish"
		allowed = "POST"
	case "/api/admin/v1/config/delivery":
		kind = "delivery"
		allowed = "GET"
	default:
		if m := roomRoute.FindStringSubmatch(p); m != nil {
			room = m[1]
			kind = m[2]
			if kind == "runtime" {
				allowed = "GET, PATCH"
			} else {
				allowed = "GET, POST"
			}
		}
		if m := eventRoute.FindStringSubmatch(p); m != nil {
			event = m[1]
			kind = "event"
			allowed = "PUT, DELETE"
			if m[2] != "" {
				allowed = "POST"
			}
		}
	}
	if kind == "" {
		problem(w, 404, "NOT_FOUND")
		return
	}
	valid := false
	for _, m := range strings.Split(allowed, ", ") {
		if r.Method == m {
			valid = true
		}
	}
	if !valid {
		w.Header().Set("Allow", allowed)
		problem(w, 405, "INVALID_REQUEST")
		return
	}
	if len(r.Header.Values("Authorization")) != 0 {
		problem(w, 401, "UNAUTHENTICATED")
		return
	}
	token := ""
	count := 0
	for _, c := range r.Cookies() {
		if c.Name == "__Host-wrs" {
			token = c.Value
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
		raw, err = io.ReadAll(io.LimitReader(r.Body, control.MaxConfigBytes+1))
	}
	if err != nil || len(raw) > control.MaxConfigBytes || (r.Method == "GET" && len(raw) > 0) {
		problem(w, 400, "INVALID_REQUEST")
		return
	}
	if r.Method != "GET" && (r.Method != "DELETE" || len(raw) > 0) {
		media, params, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if len(r.Header.Values("Content-Type")) != 1 || e != nil || media != "application/json" {
			problem(w, 400, "INVALID_REQUEST")
			return
		}
		for k, v := range params {
			if k != "charset" || !strings.EqualFold(v, "utf-8") {
				problem(w, 400, "INVALID_REQUEST")
				return
			}
		}
	}
	var out pgstore.ControlReply
	switch kind {
	case "publish":
		out, err = h.backend.Publish(r.Context(), token, r, raw)
	case "delivery":
		out, err = h.backend.View(r.Context(), token)
	case "runtime":
		if r.Method == "GET" {
			out, err = h.backend.Runtime(r.Context(), token, room)
		} else {
			out, err = h.backend.Operate(r.Context(), token, room, r, raw)
		}
	case "events":
		if r.Method == "GET" {
			out, err = h.backend.Events(r.Context(), token, room)
		} else {
			out, err = h.backend.ChangeEvent(r.Context(), token, room, "", r, raw)
		}
	case "event":
		out, err = h.backend.ChangeEvent(r.Context(), token, "", event, r, raw)
	}
	if err != nil {
		switch {
		case errors.Is(err, adminauth.ErrUnauthenticated):
			problem(w, 401, "UNAUTHENTICATED")
		case errors.Is(err, adminauth.ErrForbidden):
			problem(w, 403, "FORBIDDEN")
		default:
			problem(w, 503, "CONTROL_UNAVAILABLE")
		}
		return
	}
	if out.Status < 200 || out.Status > 599 || len(out.Body) == 0 {
		problem(w, 503, "CONTROL_UNAVAILABLE")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if out.Status >= 400 {
		w.Header().Set("Content-Type", "application/problem+json")
	}
	if out.ETag != "" {
		w.Header().Set("ETag", out.ETag)
	}
	if out.Replay {
		w.Header().Set("Idempotency-Replayed", "true")
	}
	w.WriteHeader(out.Status)
	_, _ = w.Write(out.Body)
}
