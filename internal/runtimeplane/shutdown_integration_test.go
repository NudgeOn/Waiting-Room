//go:build integration

// SPDX-License-Identifier: Apache-2.0
package runtimeplane

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync/atomic"
	"testing"
	"time"

	valkey "github.com/valkey-io/valkey-go"
	"waiting-room/internal/configtrust"
	"waiting-room/internal/control"
	"waiting-room/internal/queue/model"
	"waiting-room/internal/queue/valkeystore"
)

type drainingReplyConn struct {
	net.Conn
	armed            *atomic.Bool
	block            atomic.Bool
	entered, release chan struct{}
}

func (c *drainingReplyConn) Write(b []byte) (int, error) {
	if bytes.Contains(b, []byte("\r\nwr_r6_command\r\n")) && c.armed.CompareAndSwap(true, false) {
		c.block.Store(true)
	}
	return c.Conn.Write(b)
}
func (c *drainingReplyConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if n > 0 && c.block.CompareAndSwap(true, false) {
		close(c.entered)
		<-c.release
	}
	return n, err
}

func TestShutdownDrainsPromotionButKeepsWriteDeadline(t *testing.T) {
	if os.Getenv("WR_TEST_VALKEY") != "127.0.0.1:16379" {
		t.Fatal("dedicated Valkey required")
	}
	for _, slow := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal-drain", true: "real-deadline"}[slow], func(t *testing.T) {
			ctx := context.Background()
			owner, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{"127.0.0.1:16379"}})
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Close()
			if err = valkeystore.InstallRecoveryLibrary(ctx, owner); err != nil {
				t.Fatal(err)
			}
			var armed atomic.Bool
			entered, release := make(chan struct{}), make(chan struct{})
			defer func() {
				select {
				case <-release:
				default:
					close(release)
				}
			}()
			// Concurrent production requests use this pipeline mode. Select it
			// explicitly so a held decoder does not turn the test into a sync read.
			opt := valkey.ClientOption{InitAddress: []string{"127.0.0.1:16379"}, AlwaysPipelining: true, DialCtxFn: func(ctx context.Context, address string, dialer *net.Dialer, _ *tls.Config) (net.Conn, error) {
				conn, err := dialer.DialContext(ctx, "tcp", address)
				if err != nil {
					return nil, err
				}
				return &drainingReplyConn{Conn: conn, armed: &armed, entered: entered, release: release}, nil
			}}
			ns := fmt.Sprintf("wr:lab:shutdown-%d", time.Now().UnixNano())
			room := "abcdefghijklmnopqrst"
			cfg := model.DefaultConfig()
			q, err := valkeystore.OpenRecoveryRoom(ctx, opt, ns, room, cfg, valkeystore.StandardInstallation(), 1, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer q.Close()
			defer func() {
				keys := []string{}
				for _, s := range []string{"meta", "tickets", "waiting", "expiry", "leases", "rate", "idempotency", "idem-expiry"} {
					keys = append(keys, ns+"{"+room+":1}:"+s)
				}
				for _, s := range []string{"meta", "visitors", "idempotency"} {
					keys = append(keys, ns+"{installation:1}:"+s)
				}
				keys = append(keys, ns+"{epoch:1}:meta")
				owner.Do(ctx, owner.B().Del().Key(keys...).Build())
			}()
			if _, err = q.Configure(ctx, cfg, 1, control.Runtime{Revision: 1, Epoch: 1, Mode: "AUTO"}); err != nil {
				t.Fatal(err)
			}
			if _, err = q.Join(ctx, "retained-shutdown", "target", valkeystore.Hash("retained"), "opaque"); err != nil {
				t.Fatal(err)
			}
			pub, priv, _ := ed25519.GenerateKey(rand.Reader)
			gate, err := configtrust.Open(map[string]ed25519.PublicKey{"test": pub}, "shutdown", 1, func(json.RawMessage) error { return nil }, &configtrust.MemoryStore{})
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			raw, err := configtrust.Sign(priv, configtrust.Snapshot{SchemaVersion: 1, Installation: "shutdown", Generation: 1, Revision: 1, IssuedAt: now.Add(-time.Second).Unix(), ExpiresAt: now.Add(time.Minute).Unix(), Kid: "test", Payload: json.RawMessage(`{}`)})
			if err != nil || gate.Apply(raw) != nil {
				t.Fatal("signed gate fixture", err)
			}
			node := &Node{gate: gate, generation: 1, stores: map[string]*valkeystore.Store{room + ":1": q}, delivery: control.Delivery{Config: control.Config{Rooms: []control.Room{{PublicID: room}}}, Runtimes: []control.RoomRuntime{{Runtime: control.Runtime{Epoch: 1}}}}}
			stop, cancel := context.WithCancel(ctx)
			defer cancel()
			armed.Store(true)
			done := make(chan struct{})
			go func() { defer close(done); node.promote(stop) }()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("promotion did not reach real Valkey")
			}
			cancel()
			wait := 50 * time.Millisecond
			if slow {
				wait = 1100 * time.Millisecond
			}
			time.Sleep(wait)
			close(release)
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("promotion shutdown was unbounded")
			}
			state, err := q.MaintainRecovery(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if slow {
				if state.Mode != "RECOVERY_HOLD" || state.Reason != "uncertain_write" {
					t.Fatal("real write deadline bypassed safety", state)
				}
			} else if state.Mode != "ACTIVE" || state.Fence != 1 {
				t.Fatal("normal shutdown poisoned a completed promotion", state)
			}
			// No new operation may be submitted after shutdown is already observed.
			armed.Store(true)
			node.promote(stop)
			if !armed.Load() {
				t.Fatal("submitted a new write after shutdown")
			}
		})
	}
}
