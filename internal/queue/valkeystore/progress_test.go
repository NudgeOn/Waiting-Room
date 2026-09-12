// SPDX-License-Identifier: Apache-2.0
package valkeystore

import (
	"testing"
	"waiting-room/internal/queue/model"
)

func TestEstimateWaitRespectsPauseHistoryAndLeaseThroughput(t *testing.T) {
	c := model.DefaultConfig()
	for _, mode := range []string{"HOLD", "OFF", "RECOVERY_HOLD"} {
		if estimateWait(10, 60, c, mode) != nil {
			t.Fatal("paused queue has estimate")
		}
	}
	if estimateWait(10, 0, c, "AUTO") != nil {
		t.Fatal("missing history has estimate")
	}
	fast := estimateWait(99, 60, c, "AUTO")
	c.LeaseCap = 1
	slow := estimateWait(99, 60, c, "AUTO")
	if fast == nil || slow == nil || fast.Min < 1 || fast.Max < fast.Min || slow.Min <= fast.Min {
		t.Fatal("lease bottleneck was ignored")
	}
	if estimateWait(199, 60, c, "AUTO").Min <= slow.Min {
		t.Fatal("more visitors should increase estimate")
	}
}
