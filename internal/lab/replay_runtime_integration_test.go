//go:build integration

// SPDX-License-Identifier: Apache-2.0
package lab

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	valkey "github.com/valkey-io/valkey-go"
	"net/http/httptest"
	"os"
	"testing"
	"time"
	"waiting-room/internal/control"
	"waiting-room/internal/queue/model"
	"waiting-room/internal/queue/valkeystore"
)

func TestExpiredJoinHTTPReplaysOriginalBytesAfterConfigChange(t *testing.T) {
	address := os.Getenv("WR_TEST_RUNTIME_VALKEY")
	if address != "127.0.0.1:16389" {
		address = os.Getenv("WR_TEST_VALKEY")
	}
	if address != "127.0.0.1:16389" && address != "127.0.0.1:16379" {
		t.Fatal("dedicated test Valkey required")
	}
	ctx := context.Background()
	opt := valkey.ClientOption{InitAddress: []string{address}}
	owner, err := valkey.NewClient(opt)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if err = valkeystore.InstallRecoveryLibrary(ctx, owner); err != nil {
		t.Fatal(err)
	}
	c := model.DefaultConfig()
	c.IdleTTL = 100
	c.TicketTTL = 1000
	c.IdempotencyTTL = 600
	ns := fmt.Sprintf("wr:lab:http-replay-%d", time.Now().UnixNano())
	q, err := valkeystore.OpenRecoveryRoom(ctx, opt, ns, Room, c, valkeystore.StandardInstallation(), 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	defer func() {
		keys := []string{}
		for _, v := range []string{"meta", "tickets", "waiting", "expiry", "leases", "rate", "idempotency", "idem-expiry"} {
			keys = append(keys, ns+"{"+Room+":1}:"+v)
		}
		for _, v := range []string{"meta", "visitors", "idempotency"} {
			keys = append(keys, ns+"{installation:1}:"+v)
		}
		keys = append(keys, ns+"{epoch:1}:meta")
		owner.Do(ctx, owner.B().Del().Key(keys...).Build())
	}()
	_, signer, _ := ed25519.GenerateKey(rand.Reader)
	secret := make([]byte, 32)
	rand.Read(secret)
	service := "test-service-credential-12345678901234567890"
	makeCoordinator := func() *Coordinator {
		coord, e := NewBoundCoordinator(q, c, labBinding(), signer, secret, service)
		if e != nil {
			t.Fatal(e)
		}
		return coord
	}
	coord := makeCoordinator()
	join := func(coord *Coordinator, target string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/_wr/v1/tickets", bytes.NewBufferString(`{"target":"`+target+`"}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-WR-Service", service)
		r.Header.Set("Idempotency-Key", "http-expiry-replay-key")
		w := httptest.NewRecorder()
		coord.Handler().ServeHTTP(w, r)
		return w
	}
	first := join(coord, "/shop")
	if first.Code != 202 {
		t.Fatal("initial join", first.Code)
	}
	var original struct{ TicketToken string }
	json.Unmarshal(first.Body.Bytes(), &original)
	time.Sleep(130 * time.Millisecond)
	replay := join(coord, "/shop")
	if replay.Code != 202 || replay.Body.String() != first.Body.String() {
		t.Fatal("expired HTTP replay changed")
	}
	c.IdleTTL = 200
	c.TicketTTL = 2000
	if _, err = q.Configure(ctx, c, 1, control.Runtime{Revision: 1, Epoch: 1, Mode: "HOLD"}); err != nil {
		t.Fatal(err)
	}
	coord = makeCoordinator()
	replay = join(coord, "/shop")
	if replay.Code != 202 || replay.Body.String() != first.Body.String() || replay.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("config changed replay")
	}
	if join(coord, "/shop/other").Code != 409 {
		t.Fatal("expired fingerprint conflict")
	}
	if _, err = q.Claim(ctx, valkeystore.Hash(original.TicketToken)); err != model.ErrExpired {
		t.Fatal("replay resurrected ticket", err)
	}
	time.Sleep(500 * time.Millisecond)
	replacement := join(coord, "/shop")
	if replacement.Code != 202 || replacement.Body.String() == first.Body.String() {
		t.Fatal("idempotency expiry did not release original response")
	}
}
