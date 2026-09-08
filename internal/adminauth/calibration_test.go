// SPDX-License-Identifier: Apache-2.0
package adminauth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCalibrationTargetAndBounds(t *testing.T) {
	for _, tc := range []struct {
		name     string
		unit     time.Duration
		selected uint32
		calls    int
	}{{"target", 80 * time.Millisecond, 4, 12}, {"too-slow", 300 * time.Millisecond, 0, 4}, {"too-fast", time.Millisecond, 0, 36}} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			out, err := calibratePassword(context.Background(), func(_ context.Context, n uint32) (time.Duration, error) {
				calls++
				return time.Duration(n) * tc.unit, nil
			})
			if err != nil || out.Iterations != tc.selected || out.TargetMet != (tc.selected != 0) || calls != tc.calls {
				t.Fatalf("unexpected calibration: %+v calls %d", out, calls)
			}
		})
	}
}
func TestCalibrationFailureIsNotAResult(t *testing.T) {
	_, err := calibratePassword(context.Background(), func(context.Context, uint32) (time.Duration, error) { return 0, ErrAuthUnavailable })
	if !errors.Is(err, ErrAuthUnavailable) {
		t.Fatal("measurement failure accepted")
	}
}
func TestPasswordSourcePersistsParametersAndRejectsMismatch(t *testing.T) {
	n := uint32(3)
	h := NewPasswordHasherSource(func(context.Context) (uint32, error) { return n, nil })
	ctx := context.Background()
	stored, err := h.Hash(ctx, "local parameter preservation test")
	if err != nil {
		t.Fatal(err)
	}
	restarted := NewPasswordHasherSource(func(context.Context) (uint32, error) { return n, nil })
	if ok, err := restarted.Verify(ctx, "local parameter preservation test", stored.StorageValue()); !ok || err != nil {
		t.Fatal("restart did not load recorded parameters")
	}
	n = 2
	if ok, err := restarted.Verify(ctx, "local parameter preservation test", stored.StorageValue()); ok || !errors.Is(err, ErrAuthUnavailable) {
		t.Fatal("silently reinterpreted old credential")
	}
}
