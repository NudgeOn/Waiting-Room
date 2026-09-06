// SPDX-License-Identifier: Apache-2.0
// Package configtrust verifies complete, caller-validated configuration snapshots.
// It does not provide a Control service, key distribution, or an anti-rollback filesystem.
package configtrust

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"sync"
	"time"
)

const MaxSnapshotBytes = 65536
const domain = "waiting-room-config/v1\x00"

var ErrInvalid = errors.New("configuration snapshot invalid")
var ErrUnavailable = errors.New("trusted configuration unavailable")
var ErrRollback = errors.New("configuration generation rollback or conflict")
var ErrPersistence = errors.New("configuration persistence uncertain")
var ErrEmpty = errors.New("configuration store empty")

type Snapshot struct {
	SchemaVersion int             `json:"schemaVersion"`
	Installation  string          `json:"installation"`
	Generation    uint64          `json:"generation"`
	Revision      uint64          `json:"revision"`
	IssuedAt      int64           `json:"issuedAt"`
	ExpiresAt     int64           `json:"expiresAt"`
	Kid           string          `json:"kid"`
	Payload       json.RawMessage `json:"payload"`
}
type envelope struct {
	Snapshot  json.RawMessage `json:"snapshot"`
	Signature string          `json:"signature"`
}

// Store is dedicated to one Gate writer. Save must durably replace the complete
// signed envelope atomically. An error may mean an uncertain partial persistence.
type Store interface {
	Load() ([]byte, error)
	Save([]byte) error
}

// Validator must validate the entire payload schema and semantic constraints.
// It must reject noncanonical payload bytes and must not retain/mutate its input.
type Validator func(json.RawMessage) error

func strict(raw []byte, dst any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(dst) != nil || d.Decode(new(any)) != io.EOF {
		return ErrInvalid
	}
	canonical, e := json.Marshal(dst)
	if e != nil || !bytes.Equal(canonical, raw) {
		return ErrInvalid
	}
	return nil
}
func structural(s Snapshot) bool {
	safe := regexp.MustCompile(`^[a-zA-Z0-9_-]{1,80}$`)
	return s.SchemaVersion == 1 && safe.MatchString(s.Installation) && safe.MatchString(s.Kid) && s.Generation > 0 && s.Generation <= 9007199254740991 && s.Revision <= 9007199254740991 && s.IssuedAt > 0 && s.IssuedAt <= 253402214399 && s.ExpiresAt > s.IssuedAt && s.ExpiresAt-s.IssuedAt <= 86400 && len(s.Payload) > 1 && s.Payload[0] == '{' && json.Valid(s.Payload)
}

// Sign uses the fixed field order/encoding of Snapshot, not arbitrary JSON or JCS.
// Payload bytes must already match the application's deterministic schema encoder.
func Sign(key ed25519.PrivateKey, s Snapshot) ([]byte, error) {
	if len(key) != ed25519.PrivateKeySize || !structural(s) {
		return nil, ErrInvalid
	}
	raw, e := json.Marshal(s)
	if e != nil {
		return nil, ErrInvalid
	}
	out, e := json.Marshal(envelope{Snapshot: raw, Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, append([]byte(domain), raw...)))})
	if e != nil || len(out) > MaxSnapshotBytes {
		return nil, ErrInvalid
	}
	return out, nil
}

type Gate struct {
	mu           sync.Mutex
	keys         map[string]ed25519.PublicKey
	installation string
	minimum      uint64
	validate     Validator
	store        Store
	current      Snapshot
	raw          []byte
	lastClock    int64
	failed       bool
	now          func() time.Time
}

// Open restores even an expired signed snapshot as a high-water mark, but never
// serves it while expired. Missing storage starts cold; corrupt storage latches closed.
func Open(keys map[string]ed25519.PublicKey, installation string, minimum uint64, validate Validator, store Store) (*Gate, error) {
	if len(keys) == 0 || len(keys) > 8 || !regexp.MustCompile(`^[a-zA-Z0-9_-]{1,80}$`).MatchString(installation) || minimum == 0 || minimum > 9007199254740991 || validate == nil || store == nil {
		return nil, ErrInvalid
	}
	g := &Gate{keys: map[string]ed25519.PublicKey{}, installation: installation, minimum: minimum, validate: validate, store: store, now: time.Now}
	for kid, key := range keys {
		if !regexp.MustCompile(`^[a-zA-Z0-9_-]{1,80}$`).MatchString(kid) || len(key) != ed25519.PublicKeySize {
			return nil, ErrInvalid
		}
		g.keys[kid] = append(ed25519.PublicKey(nil), key...)
	}
	raw, e := store.Load()
	if errors.Is(e, ErrEmpty) {
		return g, nil
	}
	if e != nil {
		g.failed = true
		return g, ErrPersistence
	}
	s, e := g.verify(raw)
	if e != nil {
		g.failed = true
		return g, ErrInvalid
	}
	g.current = s
	g.raw = append([]byte(nil), raw...)
	return g, nil
}
func (g *Gate) verify(raw []byte) (Snapshot, error) {
	var out envelope
	var s Snapshot
	if len(raw) == 0 || len(raw) > MaxSnapshotBytes || strict(raw, &out) != nil || strict(out.Snapshot, &s) != nil || !structural(s) || s.Installation != g.installation || s.Generation < g.minimum {
		return Snapshot{}, ErrInvalid
	}
	key := g.keys[s.Kid]
	sig, e := base64.RawURLEncoding.DecodeString(out.Signature)
	if e != nil || len(key) != ed25519.PublicKeySize || base64.RawURLEncoding.EncodeToString(sig) != out.Signature || !ed25519.Verify(key, append([]byte(domain), out.Snapshot...), sig) {
		return Snapshot{}, ErrInvalid
	}
	if g.validate(append(json.RawMessage(nil), s.Payload...)) != nil {
		return Snapshot{}, ErrInvalid
	}
	s.Payload = append(json.RawMessage(nil), s.Payload...)
	return s, nil
}
func (g *Gate) clock(now time.Time) bool {
	n := now.Unix()
	if n < g.lastClock {
		g.failed = true
		return false
	}
	g.lastClock = n
	return true
}

// Apply samples the clock after acquiring the gate lock. Sampling in a caller
// before waiting could mistake normal goroutine reordering for clock rollback.
func (g *Gate) Apply(raw []byte) error {
	if g == nil {
		return ErrUnavailable
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.applyLocked(raw, g.now())
}
func (g *Gate) applyAt(raw []byte, now time.Time) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.applyLocked(raw, now)
}
func (g *Gate) applyLocked(raw []byte, now time.Time) error {
	if g.failed || !g.clock(now) {
		return ErrUnavailable
	}
	s, e := g.verify(raw)
	if e != nil {
		return e
	}
	if now.Unix() < s.IssuedAt || now.Unix() >= s.ExpiresAt {
		return ErrInvalid
	}
	if s.Generation < g.current.Generation || s.Revision < g.current.Revision {
		return ErrRollback
	}
	if s.Generation == g.current.Generation {
		if bytes.Equal(raw, g.raw) {
			return nil
		}
		return ErrRollback
	}
	if g.store.Save(append([]byte(nil), raw...)) != nil {
		g.failed = true
		return ErrPersistence
	}
	g.current = s
	g.raw = append([]byte(nil), raw...)
	return nil
}
func (g *Gate) Current() (Snapshot, error) {
	if g == nil {
		return Snapshot{}, ErrUnavailable
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.currentLocked(g.now())
}
func (g *Gate) currentAt(now time.Time) (Snapshot, error) {
	if g == nil {
		return Snapshot{}, ErrUnavailable
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.currentLocked(now)
}
func (g *Gate) currentLocked(now time.Time) (Snapshot, error) {
	if g.failed || !g.clock(now) || g.current.Generation == 0 || now.Unix() < g.current.IssuedAt || now.Unix() >= g.current.ExpiresAt {
		return Snapshot{}, ErrUnavailable
	}
	copy := g.current
	copy.Payload = append(json.RawMessage(nil), copy.Payload...)
	return copy, nil
}

// Handler evaluates trust for every request. The caller must bind next's complete
// configuration to the validated payload, and create a new handler on config change.
func (g *Gate) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/livez" && r.URL.RawPath == "" && r.URL.RawQuery == "" {
			w.WriteHeader(204)
			return
		}
		if _, e := g.Current(); e != nil || next == nil {
			w.Header().Set("Content-Type", "application/problem+json")
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Retry-After", "3")
			w.WriteHeader(503)
			_ = json.NewEncoder(w).Encode(map[string]any{"type": "urn:waiting-room:problem:CONFIG_UNAVAILABLE", "code": "CONFIG_UNAVAILABLE", "title": "CONFIG_UNAVAILABLE", "status": 503, "requestId": rand.Text()})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// MemoryStore is for disposable labs/tests. It is NOT restart-persistent LKG.
type MemoryStore struct {
	mu  sync.Mutex
	raw []byte
}

func (s *MemoryStore) Load() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.raw) == 0 {
		return nil, ErrEmpty
	}
	return append([]byte(nil), s.raw...), nil
}
func (s *MemoryStore) Save(raw []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.raw = append([]byte(nil), raw...)
	return nil
}
