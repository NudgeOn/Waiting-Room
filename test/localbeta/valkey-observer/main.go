//go:build ignore

// SPDX-License-Identifier: Apache-2.0
// Disposable fixture only. Never print commands, keys, values or credentials.
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

	v "github.com/valkey-io/valkey-go"
	"waiting-room/internal/localcontrol"
)

func emit(row map[string]any) {
	row["observedAt"] = time.Now().UTC().Format(time.RFC3339Nano)
	_ = json.NewEncoder(os.Stdout).Encode(row)
}
func main() {
	if len(os.Args) == 2 && os.Args[1] == "provision" {
		file := "/queue-config/users.acl"
		raw, err := os.ReadFile(file)
		if err != nil {
			os.Exit(1)
		}
		lines := strings.Split(string(raw), "\n")
		found := 0
		for i, line := range lines {
			if strings.HasPrefix(line, "user wr_initializer on ") && strings.HasSuffix(line, " +type +dump") {
				lines[i] = line + " +latency|latest"
				found++
			}
		}
		if found != 1 || os.WriteFile(file, []byte(strings.Join(lines, "\n")), 0600) != nil {
			os.Exit(1)
		}
		emit(map[string]any{"event": "fixture_diagnostic_acl", "onlyAddedCommand": "latency|latest", "role": "wr_initializer"})
		return
	}
	state, err := localcontrol.LoadState("/state")
	if err != nil {
		emit(map[string]any{"error": "private_state_unavailable"})
		os.Exit(1)
	}
	c, err := v.NewClient(v.ClientOption{InitAddress: []string{"valkey:6379"}, Username: "wr_initializer", Password: localcontrol.QueueOwnerPassword(state), DisableCache: true, DisableRetry: true, ForceSingleClient: true})
	if err != nil {
		emit(map[string]any{"error": "connection_unavailable"})
		os.Exit(1)
	}
	defer c.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, time.Hour)
	defer cancel()
	call, done := context.WithTimeout(ctx, 3*time.Second)
	err = c.Do(call, c.B().Arbitrary("LATENCY", "LATEST").Build()).Error()
	done()
	if err != nil {
		emit(map[string]any{"error": "latency_monitor_unavailable"})
		os.Exit(1)
	}
	emit(map[string]any{"event": "latency_monitor_enabled", "thresholdMs": 100, "scope": "disposable_population_fixture"})
	allowed := map[string]bool{}
	for _, key := range strings.Fields("aof_enabled aof_rewrite_in_progress aof_last_write_status aof_last_bgrewrite_status aof_delayed_fsync aof_pending_bio_fsync aof_buffer_length aof_current_size rdb_bgsave_in_progress latest_fork_usec used_memory maxmemory connected_clients blocked_clients evicted_keys instantaneous_ops_per_sec eventloop_duration_sum eventloop_duration_cmd_sum") {
		allowed[key] = true
	}
	events := map[string]bool{}
	for _, event := range strings.Fields("command fast-command aof-fsync-always aof-write aof-write-alone aof-write-pending-fsync aof-write-active-child fork expire-cycle eviction-cycle") {
		events[event] = true
	}
	seen := map[string][3]int64{}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	samples := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		call, done := context.WithTimeout(ctx, 3*time.Second)
		begin := time.Now()
		raw, err := c.Do(call, c.B().Info().Build()).ToString()
		elapsed := time.Since(begin)
		row := map[string]any{"event": "valkey_sample", "infoRoundTripMs": float64(elapsed.Microseconds()) / 1000}
		if err != nil {
			row["error"] = "info_unavailable"
		} else {
			for _, line := range strings.Split(raw, "\n") {
				key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
				if !ok || !allowed[key] {
					continue
				}
				if n, e := strconv.ParseInt(value, 10, 64); e == nil {
					row[key] = n
				} else if value == "ok" || value == "err" {
					row[key] = value
				}
			}
		}
		done()
		samples++
		if err != nil || elapsed >= 100*time.Millisecond || samples%30 == 1 {
			emit(row)
		}
		call, done = context.WithTimeout(ctx, 3*time.Second)
		latest, e := c.Do(call, c.B().Arbitrary("LATENCY", "LATEST").Build()).ToArray()
		done()
		if e != nil {
			continue
		}
		for _, entry := range latest {
			parts, e := entry.ToArray()
			if e != nil || len(parts) != 4 {
				continue
			}
			name, e := parts[0].ToString()
			if e != nil || !events[name] {
				continue
			}
			at, e := parts[1].ToInt64()
			ms, e2 := parts[2].ToInt64()
			max, e3 := parts[3].ToInt64()
			observation := [3]int64{at, ms, max}
			// A later sample in the same second can raise the maximum. A wall
			// clock correction can also move the event timestamp backwards.
			if e != nil || e2 != nil || e3 != nil || at <= 0 || ms < 0 || max < 0 || observation == seen[name] {
				continue
			}
			seen[name] = observation
			emit(map[string]any{"event": "valkey_latency", "name": name, "serverUnix": at, "latestMs": ms, "maximumMs": max})
		}
	}
}
