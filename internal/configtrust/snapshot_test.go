// SPDX-License-Identifier: Apache-2.0
package configtrust

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func fixture(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey, Snapshot) {
	t.Helper()
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	return pub, key, Snapshot{1, "install-test", 1, 1, 1000, 2000, "config-key", json.RawMessage(`{"mode":"AUTO"}`)}
}
func validatePayload(raw json.RawMessage) error {
	var p struct {
		Mode string `json:"mode"`
	}
	if strict(raw, &p) != nil || (p.Mode != "AUTO" && p.Mode != "HOLD") {
		return ErrInvalid
	}
	return nil
}
func signed(t *testing.T, key ed25519.PrivateKey, s Snapshot) []byte {
	t.Helper()
	raw, e := Sign(key, s)
	if e != nil {
		t.Fatal(e)
	}
	return raw
}
func openTest(t *testing.T, pub ed25519.PublicKey, store Store) *Gate {
	t.Helper()
	g, e := Open(map[string]ed25519.PublicKey{"config-key": pub}, "install-test", 1, validatePayload, store)
	if e != nil {
		t.Fatal(e)
	}
	return g
}

func TestSnapshotDeterministicAndStrict(t *testing.T) {
	pub, key, s := fixture(t)
	raw := signed(t, key, s)
	if !bytes.Equal(raw, signed(t, key, s)) {
		t.Fatal("nondeterministic signing")
	}
	for _, bad := range [][]byte{nil, append([]byte(" "), raw...), append(raw, byte('\n')), bytes.Replace(raw, []byte(`"generation":1`), []byte(`"generation":1,"generation":1`), 1), bytes.Replace(raw, []byte("AUTO"), []byte("HOLD"), 1), bytes.Repeat([]byte("x"), MaxSnapshotBytes+1)} {
		g := openTest(t, pub, &MemoryStore{})
		if g.applyAt(bad, time.Unix(1001, 0)) == nil {
			t.Fatal("invalid envelope accepted")
		}
	}
	g := openTest(t, pub, &MemoryStore{})
	if e := g.applyAt(raw, time.Unix(1001, 0)); e != nil {
		t.Fatal(e)
	}
	// A correctly signed but invalid application payload is still rejected.
	s.Generation = 2
	s.Payload = json.RawMessage(`{"mode":"OFF","extra":true}`)
	if g.applyAt(signed(t, key, s), time.Unix(1001, 0)) == nil {
		t.Fatal("payload schema bypass")
	}
}

func TestSnapshotTrustScopeAndBounds(t *testing.T) {
	pub, key, s := fixture(t)
	for _, edit := range []func(*Snapshot){func(s *Snapshot) { s.Installation = "other" }, func(s *Snapshot) { s.Kid = "unknown" }, func(s *Snapshot) { s.IssuedAt = 1100 }, func(s *Snapshot) { s.ExpiresAt = 1001 }} {
		copy := s
		edit(&copy)
		g := openTest(t, pub, &MemoryStore{})
		if g.applyAt(signed(t, key, copy), time.Unix(1001, 0)) == nil {
			t.Fatal("trust/time constraint bypass")
		}
	}
	for _, edit := range []func(*Snapshot){func(s *Snapshot) { s.SchemaVersion = 2 }, func(s *Snapshot) { s.Generation = 0 }, func(s *Snapshot) { s.ExpiresAt = s.IssuedAt + 86401 }, func(s *Snapshot) { s.Payload = json.RawMessage(`null`) }, func(s *Snapshot) { s.Installation = "bad/install" }} {
		copy := s
		edit(&copy)
		if _, e := Sign(key, copy); e == nil {
			t.Fatal("invalid sign accepted")
		}
	}
	if _, e := Sign(nil, s); e == nil {
		t.Fatal("missing key accepted")
	}
	keys := map[string]ed25519.PublicKey{"config-key": append(ed25519.PublicKey(nil), pub...)}
	g, e := Open(keys, "install-test", 1, validatePayload, &MemoryStore{})
	if e != nil {
		t.Fatal(e)
	}
	keys["config-key"][0] ^= 1
	if g.applyAt(signed(t, key, s), time.Unix(1001, 0)) != nil {
		t.Fatal("caller mutated pinned key")
	}
}

func TestSnapshotRollbackAndLKG(t *testing.T) {
	pub, key, s := fixture(t)
	store := &MemoryStore{}
	g := openTest(t, pub, store)
	raw1 := signed(t, key, s)
	if e := g.applyAt(raw1, time.Unix(1001, 0)); e != nil {
		t.Fatal(e)
	}
	s.Generation = 2
	raw2 := signed(t, key, s)
	if g.applyAt(raw2, time.Unix(1002, 0)) != nil {
		t.Fatal("new generation rejected")
	}
	if !errors.Is(g.applyAt(raw1, time.Unix(1002, 0)), ErrRollback) {
		t.Fatal("rollback accepted")
	}
	s.Payload = json.RawMessage(`{"mode":"HOLD"}`)
	if !errors.Is(g.applyAt(signed(t, key, s), time.Unix(1002, 0)), ErrRollback) {
		t.Fatal("same-generation equivocation accepted")
	}
	if g.applyAt(raw2, time.Unix(1002, 0)) != nil {
		t.Fatal("exact replay rejected")
	}
	if g.applyAt([]byte("invalid"), time.Unix(1002, 0)) == nil {
		t.Fatal("invalid accepted")
	}
	current, e := g.currentAt(time.Unix(1002, 0))
	if e != nil || current.Generation != 2 {
		t.Fatal("bad refresh replaced LKG")
	}
	current.Payload[0] = 'x'
	current, e = g.currentAt(time.Unix(1002, 0))
	if e != nil || current.Payload[0] != '{' {
		t.Fatal("mutable snapshot escaped")
	}
	reloaded := openTest(t, pub, store)
	if !errors.Is(reloaded.applyAt(raw1, time.Unix(1002, 0)), ErrRollback) {
		t.Fatal("restart lost high-water")
	}
	if _, e = reloaded.currentAt(time.Unix(2000, 0)); !errors.Is(e, ErrUnavailable) {
		t.Fatal("expiry accepted")
	}
	s.Generation = 3
	s.IssuedAt = 2000
	s.ExpiresAt = 2500
	if reloaded.applyAt(signed(t, key, s), time.Unix(2001, 0)) != nil {
		t.Fatal("valid refresh after expiry rejected")
	}
}

func TestSnapshotClockAndCorruptRestore(t *testing.T) {
	pub, key, s := fixture(t)
	g := openTest(t, pub, &MemoryStore{})
	if g.applyAt(signed(t, key, s), time.Unix(1001, 0)) != nil {
		t.Fatal("apply")
	}
	if _, e := g.currentAt(time.Unix(1000, 0)); e == nil {
		t.Fatal("clock rollback served")
	}
	if _, e := g.currentAt(time.Unix(1002, 0)); e == nil {
		t.Fatal("clock latch reopened")
	}
	corrupt := &MemoryStore{raw: []byte("torn")}
	bad, e := Open(map[string]ed25519.PublicKey{"config-key": pub}, "install-test", 1, validatePayload, corrupt)
	if e == nil || bad.applyAt(signed(t, key, s), time.Unix(1001, 0)) == nil {
		t.Fatal("corrupt store repaired implicitly")
	}
}

type failingStore struct{ MemoryStore }

func (*failingStore) Save([]byte) error { return errors.New("private disk error must not leak") }
func TestSnapshotPersistenceFailureAndConcurrency(t *testing.T) {
	pub, key, s := fixture(t)
	g := openTest(t, pub, &failingStore{})
	if !errors.Is(g.applyAt(signed(t, key, s), time.Unix(1001, 0)), ErrPersistence) {
		t.Fatal("persistence failure not redacted")
	}
	if _, e := g.currentAt(time.Unix(1001, 0)); e == nil {
		t.Fatal("failed persistence served")
	}
	g = openTest(t, pub, &MemoryStore{})
	var wg sync.WaitGroup
	for i := 1; i <= 40; i++ {
		copy := s
		copy.Generation = uint64(i)
		raw := signed(t, key, copy)
		wg.Add(1)
		go func() {
			defer wg.Done()
			e := g.applyAt(raw, time.Unix(1001, 0))
			if e != nil && !errors.Is(e, ErrRollback) {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	got, e := g.currentAt(time.Unix(1001, 0))
	if e != nil || got.Generation != 40 {
		t.Fatal("concurrent high-water mismatch", got.Generation, e)
	}
}

func TestSnapshotColdStartHTTP(t *testing.T) {
	pub, key, s := fixture(t)
	g := openTest(t, pub, &MemoryStore{})
	calls := 0
	h := g.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(201) }))
	for _, path := range []string{"/shop", "/_wr/v1/tickets", "/_wr/assets/wait.js", "/admin", "/livez?other=1"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 503 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(path, w.Code)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/livez", nil))
	if w.Code != 204 || calls != 0 {
		t.Fatal("cold bypass")
	}
	now := time.Now()
	s.IssuedAt = now.Unix()
	s.ExpiresAt = now.Unix() + 60
	if g.applyAt(signed(t, key, s), now) != nil {
		t.Fatal("valid activation")
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/shop", nil))
	if w.Code != 201 || calls != 1 {
		t.Fatal("valid config not applied")
	}
}

func TestSnapshotFileRestartAndPermissions(t *testing.T) {
	pub, key, s := fixture(t)
	dir := t.TempDir()
	if os.Chmod(dir, 0700) != nil {
		t.Fatal("chmod")
	}
	store, e := NewFileStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(store.Close)
	g := openTest(t, pub, store)
	s.Generation = 4
	raw := signed(t, key, s)
	if g.applyAt(raw, time.Unix(1001, 0)) != nil {
		t.Fatal("file save")
	}
	info, e := os.Stat(filepath.Join(dir, "snapshot.json"))
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatal("snapshot permissions")
	}
	store, e = NewFileStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(store.Close)
	g = openTest(t, pub, store)
	s.Generation = 3
	if !errors.Is(g.applyAt(signed(t, key, s), time.Unix(1001, 0)), ErrRollback) {
		t.Fatal("disk restart rollback")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatal("temporary file leak")
	}
	if os.Chmod(filepath.Join(dir, "snapshot.json"), 0644) != nil {
		t.Fatal("chmod")
	}
	if _, e = store.Load(); e == nil {
		t.Fatal("unsafe file permissions")
	}
	if os.Chmod(dir, 0755) != nil {
		t.Fatal("chmod")
	}
	if _, e = NewFileStore(dir); e == nil {
		t.Fatal("unsafe directory permissions")
	}
}

func TestSnapshotFileSymlinkAndOversize(t *testing.T) {
	dir := t.TempDir()
	if os.Chmod(dir, 0700) != nil {
		t.Fatal("chmod")
	}
	s, e := NewFileStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.Close)
	if os.Symlink(filepath.Join(dir, "outside"), filepath.Join(dir, "snapshot.json")) != nil {
		t.Fatal("symlink")
	}
	if _, e = s.Load(); e == nil {
		t.Fatal("symlink read")
	}
	if s.Save([]byte("x")) == nil {
		t.Fatal("symlink overwrite")
	}
	if s.Save(bytes.Repeat([]byte("x"), MaxSnapshotBytes+1)) == nil {
		t.Fatal("oversize write")
	}
}

func TestExpiredDiskSnapshotRetainsHighWater(t *testing.T) {
	pub, key, snapshot := fixture(t)
	snapshot.Generation = 4
	dir := t.TempDir()
	if os.Chmod(dir, 0700) != nil {
		t.Fatal("chmod")
	}
	store, e := NewFileStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	if store.Save(signed(t, key, snapshot)) != nil {
		t.Fatal("persist fixture")
	}
	g := openTest(t, pub, store)
	if _, e = g.currentAt(time.Unix(3000, 0)); e == nil {
		t.Fatal("expired disk snapshot served")
	}
	snapshot.Generation = 3
	snapshot.IssuedAt = 3000
	snapshot.ExpiresAt = 4000
	if !errors.Is(g.applyAt(signed(t, key, snapshot), time.Unix(3000, 0)), ErrRollback) {
		t.Fatal("expired disk high-water lost")
	}
	if store.Save([]byte(`{"snapshot":`)) != nil {
		t.Fatal("torn fixture")
	}
	damaged, e := Open(map[string]ed25519.PublicKey{"config-key": pub}, "install-test", 1, validatePayload, store)
	if e == nil {
		t.Fatal("torn file accepted")
	}
	if _, e = damaged.currentAt(time.Unix(3000, 0)); e == nil {
		t.Fatal("torn file served")
	}
}

func TestFileStoreRootSurvivesDirectoryRename(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "state")
	moved := filepath.Join(parent, "original-state")
	if os.Mkdir(dir, 0700) != nil {
		t.Fatal("mkdir")
	}
	store, e := NewFileStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	if os.Rename(dir, moved) != nil || os.Mkdir(dir, 0700) != nil {
		t.Fatal("rename fixture")
	}
	if store.Save([]byte("fixture")) != nil {
		t.Fatal("root write")
	}
	if _, e = os.Stat(filepath.Join(moved, "snapshot.json")); e != nil {
		t.Fatal("original root not used")
	}
	if _, e = os.Stat(filepath.Join(dir, "snapshot.json")); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("replacement directory used")
	}
}

func TestRuntimeClockSampleIsSerialized(t *testing.T) {
	pub, key, s := fixture(t)
	g := openTest(t, pub, &MemoryStore{})
	// Runtime reads time while owning the same lock used by the high-water check.
	// A goroutine delayed before locking cannot carry an older time into the gate.
	g.now = func() time.Time {
		if g.mu.TryLock() {
			g.mu.Unlock()
			t.Error("clock sampled outside gate lock")
		}
		return time.Unix(1001, 0)
	}
	if g.Apply(signed(t, key, s)) != nil {
		t.Fatal("runtime apply")
	}
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				if _, e := g.Current(); e != nil {
					t.Error("concurrent runtime read failed", e)
					return
				}
			}
		}()
	}
	wg.Wait()
}
