// SPDX-License-Identifier: Apache-2.0
package adminauth

import (
	"context"
	"sort"
	"time"
)

type CalibrationSample struct {
	Iterations uint32 `json:"iterations"`
	MedianMS   int64  `json:"medianMs"`
}
type Calibration struct {
	Version     int                 `json:"version"`
	MemoryKiB   int                 `json:"memoryKiB"`
	Parallelism int                 `json:"parallelism"`
	Iterations  uint32              `json:"iterations"`
	TargetMet   bool                `json:"targetMet"`
	Samples     []CalibrationSample `json:"samples"`
}

// CalibratePassword measures the actual Control process, with a fixed memory
// envelope, bounded iterations and three measurements per candidate. No user
// password is used. Missing the target is a blocker, never an automatic downgrade.
func CalibratePassword(ctx context.Context) (Calibration, error) {
	return calibratePassword(ctx, func(ctx context.Context, n uint32) (time.Duration, error) {
		start := time.Now()
		key, err := derivePassword(ctx, "installation calibration fixture", make([]byte, 16), n)
		clear(key)
		return time.Since(start), err
	})
}
func calibratePassword(ctx context.Context, measure func(context.Context, uint32) (time.Duration, error)) (Calibration, error) {
	ctx, cancel := context.WithTimeout(ctx, 24*time.Second)
	defer cancel()
	out := Calibration{Version: 1, MemoryKiB: PasswordMemoryKiB, Parallelism: 1, Samples: []CalibrationSample{}}
	for n := uint32(2); n <= 10; n++ {
		// Warm this candidate before taking the median.
		if _, err := measure(ctx, n); err != nil {
			return Calibration{}, err
		}
		times := make([]time.Duration, 3)
		for i := range times {
			var err error
			if ctx.Err() != nil {
				return Calibration{}, ErrAuthUnavailable
			}
			times[i], err = measure(ctx, n)
			if err != nil {
				return Calibration{}, err
			}
		}
		sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
		out.Samples = append(out.Samples, CalibrationSample{n, times[1].Milliseconds()})
		if times[1] >= 250*time.Millisecond && times[1] <= 500*time.Millisecond {
			out.Iterations = n
			out.TargetMet = true
			break
		}
		if times[1] > 500*time.Millisecond {
			break
		}
	}
	return out, nil
}
