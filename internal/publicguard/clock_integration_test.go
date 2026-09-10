//go:build integration

// SPDX-License-Identifier: Apache-2.0
package publicguard

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestSmallClockCorrectionPreservesQuotaAndPollDeadlines(t *testing.T) {
	g, _ := fixture(t)
	ctx := context.Background()
	source, ticket := hashed("clock-source"), hashed("clock-ticket")
	d, err := g.Check(ctx, source, "register", ticket)
	if err != nil {
		t.Fatal(err)
	}
	// A private fixture record represents a prior server observation. Neither
	// the host clock nor any production queue time/deadline is changed.
	future := d.Now + 900
	minute, reset := future/60000, (future/60000+1)*60000
	prefix := "abcdefghijklmnopqrst:1:"
	sc := fmt.Sprintf("s:%sjoin:%s:%d", prefix, source, minute)
	rc := fmt.Sprintf("r:%sjoin:%d", prefix, minute)
	poll := "p:" + prefix + ticket
	for _, cmd := range []struct {
		id, value string
		expiry    int64
	}{
		{sc, "600", reset}, {rc, "600", reset}, {poll, fmt.Sprint(future + 3000), future + 60000},
	} {
		if err := g.client.Do(ctx, g.client.B().Hset().Key(g.keys[0]).FieldValue().FieldValue(cmd.id, cmd.value).Build()).Error(); err != nil {
			t.Fatal(err)
		}
		if err := g.client.Do(ctx, g.client.B().Zadd().Key(g.keys[1]).ScoreMember().ScoreMember(float64(cmd.expiry), cmd.id).Build()).Error(); err != nil {
			t.Fatal(err)
		}
	}
	if err := g.client.Do(ctx, g.client.B().Hset().Key(g.keys[2]).FieldValue().FieldValue("clock", fmt.Sprint(future)).Build()).Error(); err != nil {
		t.Fatal(err)
	}
	limited, err := g.Check(ctx, source, "join", hashed("over-clock-budget"))
	if err != nil || limited.Allowed || limited.Now < future || limited.RetryAfterMs <= 0 {
		t.Fatalf("small correction must retain exhausted budget, got %+v, %v", limited, err)
	}
	early, err := g.Check(ctx, source, "status", ticket)
	if err != nil || early.Allowed || early.Now < future || early.RetryAfterMs < 1 {
		t.Fatal("small correction opened poll early", early, err)
	}
	heartbeat, err := g.Check(ctx, source, "heartbeat", ticket)
	if err != nil || heartbeat.Allowed || heartbeat.Now < future {
		t.Fatal("small correction must defer before reaching the queue", heartbeat, err)
	}
	value, err := g.client.Do(ctx, g.client.B().Hget().Key(g.keys[0]).Field(sc).Build()).AsInt64()
	if err != nil || value != 600 {
		t.Fatal("deferred clock correction changed exhausted quota", value, err)
	}
	if pause := time.Until(time.UnixMilli(future + 30)); pause > 0 {
		time.Sleep(pause)
	}
	heartbeat, err = g.Check(ctx, source, "heartbeat", ticket)
	if err != nil || !heartbeat.Allowed {
		t.Fatal("clock catch-up did not resume unchanged ticket budget", heartbeat, err)
	}
	// The real wait may cross the fixed-minute reset. Cleanup after catch-up
	// must expire that old window, while a still-live window remains exhausted.
	// Compare with the same server observation that performed the cleanup.
	if heartbeat.Now < reset {
		value, err = g.client.Do(ctx, g.client.B().Hget().Key(g.keys[0]).Field(sc).Build()).AsInt64()
		if err != nil || value != 600 {
			t.Fatal("clock catch-up changed a live exhausted quota", value, err)
		}
	} else if exists, err := g.client.Do(ctx, g.client.B().Hexists().Key(g.keys[0]).Field(sc).Build()).AsBool(); err != nil || exists {
		t.Fatal("clock catch-up retained an expired quota window", exists, err)
	}
}
