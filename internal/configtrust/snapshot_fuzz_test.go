// SPDX-License-Identifier: Apache-2.0
package configtrust

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"testing"
	"time"
)

func FuzzSignedSnapshot(f *testing.F) {
	// Deterministic test-only key, never used by runtime code or deployment.
	private := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, ed25519.SeedSize))
	public := private.Public().(ed25519.PublicKey)
	snapshot := Snapshot{1, "fuzz-install", 1, 1, 1000, 2000, "fuzz-key", json.RawMessage(`{"mode":"AUTO"}`)}
	valid, e := Sign(private, snapshot)
	if e != nil {
		f.Fatal(e)
	}
	f.Add(valid)
	f.Add([]byte("{}"))
	f.Add(append([]byte(" "), valid...))
	f.Add(bytes.Replace(valid, []byte("AUTO"), []byte("HOLD"), 1))
	f.Fuzz(func(t *testing.T, raw []byte) {
		g, e := Open(map[string]ed25519.PublicKey{"fuzz-key": public}, "fuzz-install", 1, func(p json.RawMessage) error {
			if !bytes.Equal(p, snapshot.Payload) {
				return ErrInvalid
			}
			return nil
		}, &MemoryStore{})
		if e != nil {
			t.Fatal(e)
		}
		now := time.Unix(1001, 0)
		if g.applyAt(valid, now) != nil {
			t.Fatal("fixture activation failed")
		}
		err := g.applyAt(raw, now)
		if err == nil && !bytes.Equal(raw, valid) {
			t.Fatal("noncanonical or forged snapshot accepted")
		}
		current, e := g.currentAt(now)
		if e != nil || current.Generation != 1 || !bytes.Equal(current.Payload, snapshot.Payload) {
			t.Fatal("rejected refresh changed LKG")
		}
	})
}
