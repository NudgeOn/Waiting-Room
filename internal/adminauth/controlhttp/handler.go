// SPDX-License-Identifier: Apache-2.0
// Package controlhttp exposes draft authoring, not runtime publish/activation.
package controlhttp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"waiting-room/internal/adminauth"
	"waiting-room/internal/adminauth/pgstore"
	"waiting-room/internal/control"
)

type Backend interface {
	Config(context.Context, string) (pgstore.ControlReply, error)
	ReplaceDraft(context.Context, string, *http.Request, []byte) (pgstore.ControlReply, error)
	AuditPage(context.Context, string, int64, int) ([]pgstore.AuditEvent, error)
}
type Handler struct{ backend Backend }

func New(b Backend) (*Handler, error) {
	if b == nil {
		return nil, adminauth.ErrAuthUnavailable
	}
	return &Handler{b}, nil
}
func problem(w http.ResponseWriter, status int, code string) {
	id, _, _ := adminauth.NewCSRFToken()
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "about:blank", "title": http.StatusText(status), "status": status, "code": code, "requestId": id})
}
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-WR-Config-State", "draft-only")
	if r.URL.EscapedPath() != r.URL.Path || r.URL.ForceQuery {
		problem(w, 400, "INVALID_REQUEST")
		return
	}
	path := r.URL.Path
	if path != "/api/admin/v1/config/draft" && path != "/api/admin/v1/audit-events" && path != "/api/admin/v1/installation" && path != "/api/admin/v1/capabilities" {
		problem(w, 404, "NOT_FOUND")
		return
	}
	if r.Method != "GET" && !(r.Method == "PUT" && path == "/api/admin/v1/config/draft") {
		w.Header().Set("Allow", "GET")
		if path == "/api/admin/v1/config/draft" {
			w.Header().Set("Allow", "GET, PUT")
		}
		problem(w, 405, "INVALID_REQUEST")
		return
	}
	before := int64(0)
	pageSize := 50
	if r.URL.RawQuery != "" {
		query, e := url.ParseQuery(r.URL.RawQuery)
		if e != nil || path != "/api/admin/v1/audit-events" || len(query) > 2 {
			problem(w, 400, "INVALID_REQUEST")
			return
		}
		for key, values := range query {
			if len(values) != 1 {
				problem(w, 400, "INVALID_REQUEST")
				return
			}
			switch key {
			case "cursor":
				raw, err := base64.RawURLEncoding.Strict().DecodeString(values[0])
				if err != nil || len(raw) > 20 {
					problem(w, 400, "INVALID_REQUEST")
					return
				}
				before, e = strconv.ParseInt(string(raw), 10, 64)
				if e != nil || before <= 0 || strconv.FormatInt(before, 10) != string(raw) {
					problem(w, 400, "INVALID_REQUEST")
					return
				}
			case "limit":
				pageSize, e = strconv.Atoi(values[0])
				if e != nil || pageSize < 1 || pageSize > 100 || strconv.Itoa(pageSize) != values[0] {
					problem(w, 400, "INVALID_REQUEST")
					return
				}
			default:
				problem(w, 400, "INVALID_REQUEST")
				return
			}
		}
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
	limit := int64(1)
	if r.Method == "PUT" {
		limit = control.MaxConfigBytes + 1
	}
	var raw []byte
	var err error
	if r.Body != nil {
		raw, err = io.ReadAll(io.LimitReader(r.Body, limit))
	}
	if err != nil || len(raw) > control.MaxConfigBytes || (r.Method == "GET" && len(raw) != 0) {
		problem(w, 400, "INVALID_REQUEST")
		return
	}
	var result pgstore.ControlReply
	if r.Method == "PUT" {
		media, params, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if len(r.Header.Values("Content-Type")) != 1 || e != nil || media != "application/json" {
			problem(w, 400, "INVALID_REQUEST")
			return
		}
		for key, value := range params {
			if key != "charset" || !strings.EqualFold(value, "utf-8") {
				problem(w, 400, "INVALID_REQUEST")
				return
			}
		}
		result, err = h.backend.ReplaceDraft(r.Context(), token, r, raw)
	} else if path == "/api/admin/v1/config/draft" {
		result, err = h.backend.Config(r.Context(), token)
	} else if path == "/api/admin/v1/capabilities" {
		result, err = h.backend.Config(r.Context(), token)
		if err == nil && result.Status == 200 {
			var config control.Config
			if json.Unmarshal(result.Body, &config) != nil {
				err = adminauth.ErrAuthUnavailable
			} else {
				result.Body, _ = json.Marshal(map[string]any{"supportedPolicies": []string{"fifo"}, "policySchemaVersion": 1, "regionMode": "single-region", "profile": config.Profile})
				result.ETag = ""
			}
		}
	} else if path == "/api/admin/v1/installation" {
		b, ok := h.backend.(interface {
			Installation(context.Context, string) (pgstore.ControlReply, error)
		})
		if !ok {
			problem(w, 404, "NOT_FOUND")
			return
		}
		result, err = b.Installation(r.Context(), token)
	} else {
		var events []pgstore.AuditEvent
		events, err = h.backend.AuditPage(r.Context(), token, before, pageSize)
		var next any
		if len(events) == pageSize {
			next = base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(events[len(events)-1].ID, 10)))
		}
		items := make([]map[string]any, 0, len(events))
		for _, a := range events {
			items = append(items, map[string]any{"id": strconv.FormatInt(a.ID, 10), "at": a.OccurredAt, "actorId": a.ActorID, "actorRole": a.ActorRole, "action": a.Action, "targetId": a.TargetID, "beforeDigest": a.BeforeDigest, "afterDigest": a.AfterDigest, "result": a.Result, "requestId": a.RequestID, "revision": a.Revision})
		}
		result.Status = 200
		result.Body, _ = json.Marshal(map[string]any{"items": items, "nextCursor": next})
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
	if result.Status < 200 || result.Status > 599 || len(result.Body) == 0 {
		problem(w, 503, "CONTROL_UNAVAILABLE")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if result.Status >= 400 {
		w.Header().Set("Content-Type", "application/problem+json")
	}
	if result.ETag != "" {
		w.Header().Set("ETag", result.ETag)
	}
	if result.Replay {
		w.Header().Set("Idempotency-Replayed", "true")
	}
	w.WriteHeader(result.Status)
	_, _ = w.Write(result.Body)
}
