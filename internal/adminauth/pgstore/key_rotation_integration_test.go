//go:build integration

// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"testing"
	"waiting-room/internal/keyring"
)

func TestDeploymentKeyACKAndSignerRefresh(t *testing.T) {
	f, s, _ := publicationFixture(t)
	execSQL(t, f.pool, Migration014)
	ctx := context.Background()
	old, err := s.Envelope(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	ad, _, _ := ed25519.GenerateKey(rand.Reader)
	keys := keyring.Set{Generation: 1, Phase: "stable", Current: keyring.Key{ID: "11111111111111111111111111111111", ConfigPublic: pub, ConfigPrivate: priv, AdmissionPublic: ad}}
	if err = s.WithDeploymentKeys(&keys); err != nil {
		t.Fatal(err)
	}
	if err = s.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	current, err := s.Envelope(ctx)
	if err != nil || string(old) == string(current) {
		t.Fatal("key change must refresh immediately", err)
	}
	var e struct {
		Snapshot struct {
			Generation int64
			Kid        string
		}
	}
	if json.Unmarshal(current, &e) != nil || e.Snapshot.Kid != keys.Current.ConfigKid() {
		t.Fatal("wrong signer")
	}
	ack := NodeAck{Generation: e.Snapshot.Generation, Digest: digest(current), Rooms: []RoomMetrics{{RoomID: "sale", Revision: 1, Epoch: 1, Mode: "HOLD"}}, KeyGeneration: 1, KeyDigest: keys.Digest()}
	stale := ack
	stale.KeyGeneration = 0
	if s.Acknowledge(ctx, "gateway", stale) == nil {
		t.Fatal("stale keys ACK")
	}
	if err = s.Acknowledge(ctx, "gateway", ack); err != nil {
		t.Fatal(err)
	}
	if err = s.Acknowledge(ctx, "coordinator", ack); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = f.pool.QueryRow(ctx, "SELECT count(*) FROM control_key_acks WHERE generation=1").Scan(&n); err != nil || n != 2 {
		t.Fatal("durable key ACK", err)
	}
}
