// SPDX-License-Identifier: Apache-2.0
// Measures this host only. Prints public parameters, never stores or changes credentials.
package main

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"time"
	"waiting-room/internal/adminauth"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	type sample struct {
		Iterations uint32
		MedianMS   int64
	}
	samples := []sample{}
	var selected uint32
	for n := uint32(2); n <= 10; n++ {
		hasher, err := adminauth.NewPasswordHasher(ctx, n)
		if err != nil {
			os.Exit(1)
		}
		times := make([]time.Duration, 3)
		for i := range times {
			start := time.Now()
			_, err = hasher.Hash(ctx, "local calibration fixture only")
			if err != nil {
				os.Exit(1)
			}
			times[i] = time.Since(start)
		}
		sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
		samples = append(samples, sample{n, times[1].Milliseconds()})
		if times[1] >= 250*time.Millisecond && times[1] <= 500*time.Millisecond {
			selected = n
			break
		}
	}
	report := struct {
		Scope       string
		MemoryKiB   int
		Parallelism int
		Iterations  uint32
		TargetMet   bool
		Samples     []sample
	}{
		"local-host-only-not-installation-qualified", adminauth.PasswordMemoryKiB, 1, selected, selected != 0, samples,
	}
	if json.NewEncoder(os.Stdout).Encode(report) != nil {
		os.Exit(1)
	}
	if selected == 0 {
		os.Exit(1)
	}
}
