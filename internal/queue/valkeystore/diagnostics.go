// SPDX-License-Identifier: Apache-2.0
package valkeystore

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

type callDiagnostic struct {
	mu sync.Mutex
	at time.Time
}

// Diagnostics contain bounded enums and durations only, never error text,
// command arguments, keys, primary identities, ticket IDs or credentials.
func (d *callDiagnostic) observe(op string, read bool, err error, elapsed, lockWait, budget time.Duration, now time.Time) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.at.IsZero() && now.Sub(d.at) >= 0 && now.Sub(d.at) < time.Minute {
		return ""
	}
	d.at = now
	switch op {
	case "join", "promote", "claim", "heartbeat", "configure", "sweep", "status", "capacity", "metrics":
	default:
		op = "unknown"
	}
	code := "response_invalid"
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			code = "deadline"
		case errors.Is(err, context.Canceled):
			code = "cancelled"
		case strings.Contains(err.Error(), "WR_SCHEMA"):
			code = "server_schema"
		case strings.Contains(err.Error(), "WR_UNAVAILABLE"):
			code = "server_unavailable"
		case readTransportFailure(err):
			code = "transport"
		default:
			code = "server_or_transport"
		}
	}
	return fmt.Sprintf("queue_call state=uncertain operation=%s read=%t code=%s elapsed_ms=%d lock_wait_ms=%d budget_ms=%d", op, read, code, elapsed.Milliseconds(), lockWait.Milliseconds(), budget.Milliseconds())
}

func (s *Store) diagnoseCall(op string, read bool, err error, began time.Time, lockWait, budget time.Duration) {
	now := time.Now()
	if line := s.diagnostic.observe(op, read, err, now.Sub(began), lockWait, budget, now); line != "" {
		log.Print(line)
	}
}
