//go:build integration

// SPDX-License-Identifier: Apache-2.0
package valkeystore

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	valkey "github.com/valkey-io/valkey-go"
	"waiting-room/internal/queue/model"
)

// Drop the real server reply after a selected command has reached Valkey.
// This tests a disconnected socket, without mocking the store result or
// changing server time, fencing metadata or persisted queue records.
type lostReadReply struct {
	match []byte
	armed atomic.Bool
	used  atomic.Bool
}

type lostReadConn struct {
	net.Conn
	fault *lostReadReply
	drop  atomic.Bool
}

func (c *lostReadConn) Write(b []byte) (int, error) {
	if c.fault.armed.Load() && bytes.Contains(b, c.fault.match) && c.fault.used.CompareAndSwap(false, true) {
		c.drop.Store(true)
	}
	return c.Conn.Write(b)
}

func (c *lostReadConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if n > 0 && c.drop.CompareAndSwap(true, false) {
		_ = c.Conn.Close()
		return 0, io.ErrUnexpectedEOF
	}
	return n, err
}

func loseOneReply(t *testing.T, s *Store, command string) *lostReadReply {
	t.Helper()
	fault := &lostReadReply{match: []byte("\r\n" + command + "\r\n")}
	client, err := valkey.NewClient(valkey.ClientOption{
		InitAddress: []string{runtimeTestAddress(t)}, ForceSingleClient: true, DisableCache: true, DisableRetry: true,
		DialCtxFn: func(ctx context.Context, address string, dialer *net.Dialer, _ *tls.Config) (net.Conn, error) {
			conn, err := dialer.DialContext(ctx, "tcp", address)
			if err != nil {
				return nil, err
			}
			return &lostReadConn{Conn: conn, fault: fault}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	original := s.client
	s.client = client
	// Registered after the store fixture cleanup, so restore its client first.
	t.Cleanup(func() { s.client = original; client.Close() })
	fault.armed.Store(true)
	return fault
}

func TestLostReadReplyDoesNotPoisonUnrelatedVisitors(t *testing.T) {
	for _, tc := range []struct {
		name, command string
		runtime       bool
	}{
		{"legacy-primary-read", "INFO", false},
		{"legacy-status-read", "wr_i2_status", false},
		{"runtime-status-read", "wr_r6_status", true},
		{"runtime-metrics-read", "wr_r6_metrics", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			config := model.DefaultConfig()
			var s *Store
			if tc.runtime {
				s, _ = recoveryStore(t, config)
			} else {
				stores, _ := installationStores(t, 1, config, StandardInstallation())
				s = stores[0]
			}
			first := add(t, s, "retained-before-read-loss")
			fault := loseOneReply(t, s, tc.command)
			var err error
			if tc.command == "wr_r6_metrics" {
				_, err = s.Metrics(ctx)
			} else {
				_, err = s.Status(ctx, first.Ticket.ID)
			}
			if err == nil || !fault.used.Load() {
				t.Fatal("did not lose the selected real read reply")
			}
			if s.failed.Load() || s.uncertainty.Load() != 0 {
				t.Fatal("one read-only disconnection poisoned the entire store")
			}
			var next Result
			for ctx.Err() == nil {
				next, err = s.Status(ctx, first.Ticket.ID)
				if err == nil {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if err != nil || next.Ticket == nil || next.Ticket.Sequence != first.Ticket.Sequence {
				t.Fatal("same client did not recover its retained ticket", err)
			}
			joined := add(t, s, "unrelated-after-read-loss")
			if joined.Ticket.Sequence != first.Ticket.Sequence+1 {
				t.Fatal("read loss changed queue order or rejected another visitor")
			}
			if tc.runtime {
				state, err := s.MaintainRecovery(ctx)
				if err != nil || state.Mode != "ACTIVE" || state.Fence != 1 {
					t.Fatal("read-only failure created a shared recovery hold", state, err)
				}
			}
		})
	}
}

func TestLostWriteReplyStillHoldsInstallation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, _ := recoveryStore(t, model.DefaultConfig())
	fault := loseOneReply(t, s, "wr_r6_command")
	_, err := s.Join(ctx, "lost-write", "target", Hash("lost-write-ticket"), "opaque")
	if err == nil || !fault.used.Load() || s.uncertainty.Load() == 0 {
		t.Fatal("uncertain write was treated as a harmless read")
	}
	state, err := s.MaintainRecovery(ctx)
	if err != nil || state.Mode != "RECOVERY_HOLD" || state.Reason != "uncertain_write" || state.UnsafeUntil <= state.Now {
		t.Fatal("lost write did not keep the real safety deadline", state, err)
	}
	if _, err = s.Promote(ctx, 1); !errors.Is(err, model.ErrUnavailable) {
		t.Fatal("lost-write hold allowed admission", err)
	}
}

func TestLostLegacyWriteReplyStillLatches(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stores, _ := installationStores(t, 1, model.DefaultConfig(), StandardInstallation())
	s := stores[0]
	fault := loseOneReply(t, s, "wr_i2_command")
	_, err := s.Join(ctx, "lost-write", "target", Hash("lost-write-ticket"), "opaque")
	if !errors.Is(err, model.ErrUnavailable) || !fault.used.Load() || !s.failed.Load() {
		t.Fatal("legacy uncertain write did not latch")
	}
	if _, err = s.Promote(ctx, 1); !errors.Is(err, model.ErrUnavailable) {
		t.Fatal("legacy lost-write latch allowed admission", err)
	}
}

func TestReadServerInvariantFailureStillHolds(t *testing.T) {
	for _, current := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "runtime"}[current], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var s *Store
			if current {
				s, _ = recoveryStore(t, model.DefaultConfig())
			} else {
				stores, _ := installationStores(t, 1, model.DefaultConfig(), StandardInstallation())
				s = stores[0]
			}
			first := add(t, s, "before-invariant-failure")
			// Corrupt only this fixture's ticket hash type. The real read-only
			// function must report an invariant failure, not a transport failure.
			if err := s.client.Do(ctx, s.client.B().Set().Key(s.keys[1]).Value("invalid-ticket-type").Build()).Error(); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Status(ctx, first.Ticket.ID); !errors.Is(err, model.ErrUnavailable) {
				t.Fatal("server read error did not fail closed", err)
			}
			if current {
				state, err := s.MaintainRecovery(ctx)
				if err != nil || s.uncertainty.Load() == 0 || state.Mode != "RECOVERY_HOLD" {
					t.Fatal("runtime invariant failure bypassed recovery", state, err)
				}
			} else if !s.failed.Load() {
				t.Fatal("legacy invariant failure bypassed latch")
			}
			if _, err := s.Promote(ctx, 1); !errors.Is(err, model.ErrUnavailable) {
				t.Fatal("invalid server data allowed admission", err)
			}
		})
	}
}
