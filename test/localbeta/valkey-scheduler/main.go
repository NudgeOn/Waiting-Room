//go:build ignore

// SPDX-License-Identifier: Apache-2.0
// Read-only scheduler observer for one disposable Valkey PID namespace.
package main

import (
	"context"
	"encoding/json"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func read() []int64 {
	b, e := os.ReadFile("/proc/1/schedstat")
	if e != nil {
		return nil
	}
	f := strings.Fields(string(b))
	if len(f) != 3 {
		return nil
	}
	n := make([]int64, 3)
	for i := range f {
		n[i], e = strconv.ParseInt(f[i], 10, 64)
		if e != nil {
			return nil
		}
	}
	return n
}
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	e := json.NewEncoder(os.Stdout)
	old := read()
	if old == nil {
		e.Encode(map[string]any{"error": "schedstat_unavailable"})
		return
	}
	e.Encode(map[string]any{"event": "scheduler_observer_ready", "target": "disposable_valkey_pid1", "observedAt": time.Now().UTC()})
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	last := time.Now()
	report := last
	run, wait, count := int64(0), int64(0), int64(0)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		now := time.Now()
		v := read()
		if v == nil {
			return
		}
		dr, dw, dc := v[0]-old[0], v[1]-old[1], v[2]-old[2]
		run += dr
		wait += dw
		count += dc
		if dw > 50e6 || now.Sub(last) > 250*time.Millisecond || now.Sub(report) >= 30*time.Second {
			e.Encode(map[string]any{"event": "valkey_scheduler", "observedAt": now.UTC(), "elapsedMs": float64(now.Sub(last).Microseconds()) / 1000, "runNs": dr, "waitNs": dw, "timeslices": dc, "windowRunNs": run, "windowWaitNs": wait, "windowTimeslices": count, "windowMs": float64(now.Sub(report).Microseconds()) / 1000})
			report = now
			run = 0
			wait = 0
			count = 0
		}
		old = v
		last = now
	}
}
