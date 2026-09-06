// SPDX-License-Identifier: Apache-2.0
package admission

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"
)

func TestDeterministicAdmissionAndBoundaries(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	c := Claims{Kid: "lab", Room: "room", Epoch: 1, JTI: "one", Issued: 100, NotBefore: 100, Expires: 160, Audience: "shop"}
	token, e := Sign(priv, c)
	if e != nil {
		t.Fatal(e)
	}
	retry, _ := Sign(priv, c)
	if token != retry {
		t.Fatal("non deterministic")
	}
	for _, at := range []int64{100, 159, 189} {
		if _, e = Verify(pub, token, "lab", "room", "shop", 1, time.Unix(at, 0), 30*time.Second); e != nil {
			t.Fatal(at, e)
		}
	}
	for _, at := range []int64{69, 190} {
		if _, e = Verify(pub, token, "lab", "room", "shop", 1, time.Unix(at, 0), 30*time.Second); e == nil {
			t.Fatal("time accepted", at)
		}
	}
	for _, values := range [][3]string{{"wrong", "room", "shop"}, {"lab", "wrong", "shop"}, {"lab", "room", "wrong"}} {
		if _, e = Verify(pub, token, values[0], values[1], values[2], 1, time.Unix(120, 0), 0); e == nil {
			t.Fatal("binding accepted")
		}
	}
	if _, e = Verify(pub, token, "lab", "room", "shop", 2, time.Unix(120, 0), 0); e == nil {
		t.Fatal("epoch")
	}
	if _, e = Verify(pub, token+"x", "lab", "room", "shop", 1, time.Unix(120, 0), 0); e == nil {
		t.Fatal("tamper")
	}
	if _, e = Sign(nil, c); e == nil {
		t.Fatal("invalid key")
	}
}
