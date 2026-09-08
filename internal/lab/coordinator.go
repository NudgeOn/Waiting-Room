// SPDX-License-Identifier: Apache-2.0
// Package lab implements an isolated app-only walking skeleton, not production listeners.
package lab

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"waiting-room/internal/admission"
	"waiting-room/internal/publicguard"
	"waiting-room/internal/queue/model"
	"waiting-room/internal/queue/valkeystore"
)

const Room = "abcdefghijklmnopqrst"
const Audience = "wr-local-lab-origin"
const base = "/_wr/v1/rooms/" + Room

type Queue interface {
	Join(context.Context, string, string, string, string) (valkeystore.Result, error)
	Status(context.Context, string) (valkeystore.Result, error)
	Claim(context.Context, string) (valkeystore.Result, error)
	Heartbeat(context.Context, string) (valkeystore.Result, error)
	Promote(context.Context, int) (valkeystore.Result, error)
}
type Coordinator struct {
	Guard      publicguard.CheckFunc
	queue      Queue
	config     model.Config
	private    ed25519.PrivateKey
	Public     ed25519.PublicKey
	ServiceKey string
	replay     cipher.AEAD
	binding    Binding
}

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func NewCoordinator(q Queue, c model.Config) (*Coordinator, error) {
	if _, err := model.New(c); err != nil {
		return nil, err
	}
	if c.ClockSkew != 30000 {
		return nil, errors.New("lab Gateway requires 30-second reserved verifier leeway")
	}
	public, private, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		return nil, e
	}
	b := make([]byte, 32)
	if _, e = rand.Read(b); e != nil {
		return nil, e
	}
	block, e := aes.NewCipher(b)
	if e != nil {
		return nil, e
	}
	aead, e := cipher.NewGCM(block)
	if e != nil {
		return nil, e
	}
	return &Coordinator{queue: q, config: c, private: private, Public: public, ServiceKey: randomToken(), replay: aead, binding: labBinding()}, nil
}

// Target is deliberately conservative in M1; encoded paths arrive with the M2 normalizer.
func ValidTarget(target string) bool {
	if len(target) == 0 || len(target) > 2048 || !strings.HasPrefix(target, "/") || strings.HasPrefix(target, "//") {
		return false
	}
	for _, r := range target {
		if r < 32 || r == 127 || r == '\\' {
			return false
		}
	}
	u, e := url.ParseRequestURI(target)
	if e != nil || u.IsAbs() || u.Host != "" || u.Fragment != "" || u.RawPath != "" {
		return false
	}
	for _, seg := range strings.Split(u.Path, "/") {
		if seg == "." || seg == ".." || strings.Contains(seg, "%") || strings.Contains(seg, "\\") {
			return false
		}
	}
	return u.Path == "/shop" || strings.HasPrefix(u.Path, "/shop/")
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func problem(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Cache-Control", "no-store")
	if status == 429 || status == 503 {
		w.Header().Set("Retry-After", "3")
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "urn:waiting-room:problem:" + code, "code": code, "title": code, "status": status, "requestId": randomToken()})
}
func queueError(w http.ResponseWriter, e error) {
	status, code := 503, "QUEUE_UNAVAILABLE"
	switch {
	case errors.Is(e, model.ErrExpired):
		status, code = 410, "TICKET_EXPIRED"
	case errors.Is(e, model.ErrConflict):
		status, code = 409, "IDEMPOTENCY_CONFLICT"
	case errors.Is(e, model.ErrCapacity):
		code = "QUEUE_CAPACITY_EXCEEDED"
	case errors.Is(e, model.ErrDrain):
		code = "QUEUE_DRAINING"
	case errors.Is(e, model.ErrNotReady):
		status, code = 409, "TICKET_NOT_READY"
	}
	problem(w, status, code)
}
func (c *Coordinator) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /_wr/v1/tickets", c.join)
	mux.HandleFunc("GET "+c.binding.base()+"/status", c.ticket("status"))
	mux.HandleFunc("POST "+c.binding.base()+"/admissions", c.ticket("claim"))
	mux.HandleFunc("POST "+c.binding.base()+"/heartbeat", c.ticket("heartbeat"))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.Header.Values("X-WR-Service")) != 1 || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-WR-Service")), []byte(c.ServiceKey)) != 1 {
			problem(w, 401, "UNAUTHENTICATED")
			return
		}
		method := map[string]string{"/_wr/v1/tickets": "POST", c.binding.base() + "/status": "GET", c.binding.base() + "/admissions": "POST", c.binding.base() + "/heartbeat": "POST"}[r.URL.Path]
		if method == "" {
			problem(w, 404, "NOT_FOUND")
			return
		}
		if r.Method != method {
			w.Header().Set("Allow", method)
			problem(w, 405, "METHOD_NOT_ALLOWED")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		mux.ServeHTTP(w, r.WithContext(ctx))
	})
}
func (c *Coordinator) queued(t model.Ticket, token string) map[string]any {
	out := map[string]any{"apiVersion": "v1", "state": "queued", "roomId": c.binding.Room, "pollAfterMs": 3000, "heartbeatAfterMs": max(int64(1), min(int64(300000), c.config.IdleTTL/2)), "usersAhead": nil, "estimatedWaitSeconds": nil, "expiresAt": time.UnixMilli(t.JoinedAt + c.config.IdleTTL).UTC().Format(time.RFC3339Nano), "statusUrl": c.binding.base() + "/status"}
	if token != "" {
		out["ticketToken"] = token
	}
	return out
}
func (c *Coordinator) join(w http.ResponseWriter, r *http.Request) {
	if !jsonContentType(r.Header) {
		problem(w, 400, "INVALID_REQUEST")
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if len(r.Header.Values("Idempotency-Key")) != 1 || len(key) < 16 || len(key) > 128 {
		problem(w, 400, "INVALID_REQUEST")
		return
	}
	target, err := joinTargetFor(w, r, c.binding.ValidTarget)
	if err != nil {
		problem(w, 400, "INVALID_REQUEST")
		return
	}
	if _, ok := c.checkPublic(w, r, "join", valkeystore.Hash(key)); !ok {
		return
	}
	token := randomToken()
	nonce := make([]byte, c.replay.NonceSize())
	_, _ = rand.Read(nonce)
	aad := []byte(valkeystore.Hash(key) + ":" + valkeystore.Hash(target))
	encrypted := c.replay.Seal(nonce, nonce, []byte(token), aad)
	result, e := c.queue.Join(r.Context(), key, target, valkeystore.Hash(token), base64.RawURLEncoding.EncodeToString(encrypted))
	if e != nil {
		queueError(w, e)
		return
	}
	data, e := base64.RawURLEncoding.DecodeString(result.Replay)
	if e != nil || len(data) < c.replay.NonceSize() {
		problem(w, 503, "QUEUE_UNAVAILABLE")
		return
	}
	plain, e := c.replay.Open(nil, data[:c.replay.NonceSize()], data[c.replay.NonceSize():], aad)
	if e != nil {
		problem(w, 503, "QUEUE_UNAVAILABLE")
		return
	}
	_, ok := c.checkPublic(w, r, "register", result.Ticket.ID)
	if !ok {
		return
	}
	// Always replay the original queued join response. Poll is the current-state endpoint.
	w.Header().Set(absoluteHeader, strconv.FormatInt(result.Ticket.AbsoluteUntil, 10))
	out := c.queued(*result.Ticket, string(plain))
	// Keep the original join hint stable across upgrades. Status and 429 expose
	// the current shared schedule; an old join replay must remain byte-identical.
	writeJSON(w, 202, out)
}
func (c *Coordinator) ticket(op string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" {
			problem(w, 400, "INVALID_REQUEST")
			return
		}
		if len(r.Header.Values("Authorization")) > 1 {
			problem(w, 400, "INVALID_REQUEST")
			return
		}
		auth := strings.Fields(r.Header.Get("Authorization"))
		if len(auth) != 2 || auth[0] != "Bearer" {
			problem(w, 401, "UNAUTHENTICATED")
			return
		}
		decoded, e := base64.RawURLEncoding.DecodeString(auth[1])
		if e != nil || len(decoded) != 32 {
			problem(w, 401, "UNAUTHENTICATED")
			return
		}
		id := valkeystore.Hash(auth[1])
		decision, ok := c.checkPublic(w, r, op, id)
		if !ok {
			return
		}
		var result valkeystore.Result
		switch op {
		case "status":
			result, e = c.queue.Status(r.Context(), id)
		case "heartbeat":
			result, e = c.queue.Heartbeat(r.Context(), id)
		case "claim":
			result, e = c.queue.Claim(r.Context(), id)
		}
		if e != nil {
			queueError(w, e)
			return
		}
		if op == "heartbeat" {
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(204)
			return
		}
		t := result.Ticket
		w.Header().Set(absoluteHeader, strconv.FormatInt(t.AbsoluteUntil, 10))
		if op == "claim" {
			issued := t.PromotedAt
			if issued == 0 {
				issued = t.AdmissionUntil - c.config.AdmissionTTL
			}
			claims := admission.Claims{Kid: c.binding.Kid, Room: c.binding.Room, Epoch: t.Epoch, JTI: t.JTI, Issued: issued / 1000, NotBefore: issued / 1000, Expires: t.AdmissionUntil / 1000, Audience: c.binding.Audience}
			token, e := admission.Sign(c.private, claims)
			if e != nil {
				problem(w, 503, "QUEUE_UNAVAILABLE")
				return
			}
			writeJSON(w, 200, map[string]any{"apiVersion": "v1", "state": "admitted", "roomId": c.binding.Room, "admissionToken": token, "expiresAt": time.Unix(claims.Expires, 0).UTC().Format(time.RFC3339)})
			return
		}
		if t.State == model.Waiting {
			out := c.queued(*t, "")
			if c.Guard != nil {
				out["pollAfterMs"] = decision.PollAfterMs
			}
			out["expiresAt"] = time.UnixMilli(t.IdleUntil).UTC().Format(time.RFC3339Nano)
			writeJSON(w, 202, out)
			return
		}
		out := map[string]any{"apiVersion": "v1", "state": strings.ToLower(string(t.State)), "roomId": c.binding.Room, "expiresAt": time.UnixMilli(t.AdmissionUntil).UTC().Format(time.RFC3339Nano)}
		if t.State == model.Ready {
			out["claimUrl"] = c.binding.base() + "/admissions"
			out["expiresAt"] = time.UnixMilli(t.ReadyUntil).UTC().Format(time.RFC3339Nano)
		}
		writeJSON(w, 200, out)
	}
}

// checkPublic runs only after protocol credential validation and before a queue call.
func (c *Coordinator) checkPublic(w http.ResponseWriter, r *http.Request, op, id string) (publicguard.Decision, bool) {
	if c.Guard == nil {
		return publicguard.Decision{}, true
	}
	if len(r.Header.Values("X-WR-Source")) != 1 {
		problem(w, 400, "INVALID_REQUEST")
		return publicguard.Decision{}, false
	}
	d, err := c.Guard(r.Context(), r.Header.Get("X-WR-Source"), op, id)
	if err != nil {
		problem(w, 503, "QUEUE_UNAVAILABLE")
		return d, false
	}
	if !d.Allowed {
		problemWithRetry(w, 429, "API_RATE_LIMITED", max(int64(1), (d.RetryAfterMs+999)/1000))
		return d, false
	}
	return d, true
}
func problemWithRetry(w http.ResponseWriter, status int, code string, seconds int64) {
	w.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "urn:waiting-room:problem:" + code, "code": code, "title": code, "status": status, "requestId": randomToken()})
}
