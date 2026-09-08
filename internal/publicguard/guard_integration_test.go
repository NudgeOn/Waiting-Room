//go:build integration

// SPDX-License-Identifier: Apache-2.0
package publicguard

import (
	"context"
	"crypto/sha256"
	"fmt"
	v "github.com/valkey-io/valkey-go"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func hashed(s string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(s))) }
func fixture(t *testing.T) (*Guard, *Guard) {
	t.Helper()
	address := os.Getenv("WR_TEST_RUNTIME_VALKEY")
	if address != "127.0.0.1:16389" {
		address = os.Getenv("WR_TEST_VALKEY")
	}
	if address != "127.0.0.1:16389" && address != "127.0.0.1:16379" {
		t.Fatal("dedicated Valkey required")
	}
	opt := v.ClientOption{InitAddress: []string{address}}
	owner, e := v.NewClient(opt)
	if e != nil {
		t.Fatal(e)
	}
	defer owner.Close()
	if e = Install(context.Background(), owner); e != nil {
		t.Fatal(e)
	}
	ns := fmt.Sprintf("wr:lab:guard-%d", time.Now().UnixNano())
	a, e := Open(context.Background(), opt, ns, "abcdefghijklmnopqrst", 1, 10000)
	if e != nil {
		t.Fatal(e)
	}
	b, e := Open(context.Background(), opt, ns, "abcdefghijklmnopqrst", 1, 10000)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		a.client.Do(context.Background(), a.client.B().Del().Key(a.keys...).Build())
		a.Close()
		b.Close()
	})
	return a, b
}
func TestSharedPollScheduleRejectsEarlyAndConcurrentReads(t *testing.T) {
	a, b := fixture(t)
	ctx := context.Background()
	source, id := hashed("source"), hashed("ticket")
	first, e := a.Check(ctx, source, "register", id)
	if e != nil || !first.Allowed || first.PollAfterMs < 3000 || first.PollAfterMs > 20000 {
		t.Fatal("register", first, e)
	}
	early, e := b.Check(ctx, source, "status", id)
	if e != nil || early.Allowed || early.RetryAfterMs < 1 {
		t.Fatal("other coordinator allowed early poll", early, e)
	}
	replay, e := b.Check(ctx, source, "register", id)
	if e != nil || !replay.Allowed || replay.PollAfterMs != first.PollAfterMs || replay.RetryAfterMs > first.RetryAfterMs {
		t.Fatal("registration retry extended poll window", replay, e)
	}
	time.Sleep(time.Duration(early.RetryAfterMs+30) * time.Millisecond)
	var allowed, failures atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			g := a
			if i%2 == 1 {
				g = b
			}
			out, e := g.Check(ctx, source, "status", id)
			if e != nil {
				failures.Add(1)
			} else if out.Allowed {
				allowed.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if allowed.Load() != 1 || failures.Load() != 0 {
		t.Fatal("concurrent requests escaped one shared schedule", allowed.Load(), failures.Load())
	}
	if d, e := a.Check(ctx, source, "claim", id); e != nil || !d.Allowed {
		t.Fatal("poll schedule blocked claim", e)
	}
}
func TestSourceQuotaRetryAndOtherSourceIsolation(t *testing.T) {
	a, b := fixture(t)
	ctx := context.Background()
	source := hashed("source")
	// Start clear of a minute boundary so this test measures exactly one window.
	now, e := a.client.Do(ctx, a.client.B().Time().Build()).ToArray()
	if e != nil {
		t.Fatal(e)
	}
	sec, _ := now[0].AsInt64()
	if sec%60 > 55 {
		time.Sleep(time.Duration(61-sec%60) * time.Second)
	}
	for i := 0; i < 600; i++ {
		g := a
		if i%2 == 1 {
			g = b
		}
		if d, e := g.Check(ctx, source, "join", hashed(fmt.Sprint(i))); e != nil || !d.Allowed {
			t.Fatal("below source quota", i, e)
		}
	}
	if d, e := b.Check(ctx, source, "join", hashed("over")); e != nil || d.Allowed || d.RetryAfterMs < 1 {
		t.Fatal("source quota escaped", d, e)
	}
	if d, e := b.Check(ctx, source, "join", hashed("0")); e != nil || !d.Allowed {
		t.Fatal("known idempotent retry blocked by new-join quota", e)
	}
	if d, e := b.Check(ctx, hashed("other-source"), "join", hashed("other")); e != nil || !d.Allowed {
		t.Fatal("unrelated source blocked", e)
	}
	if d, e := a.Check(ctx, source, "heartbeat", hashed("ticket")); e != nil || !d.Allowed {
		t.Fatal("new join quota blocked existing ticket", e)
	}
}
func TestGuardReconnectSpreadAndClockFailure(t *testing.T) {
	g, _ := fixture(t)
	ctx := context.Background()
	source := hashed("source")
	delays := map[int64]bool{}
	for i := 0; i < 20; i++ {
		d, e := g.Check(ctx, source, "status", hashed(fmt.Sprint(i)))
		if e != nil || d.Allowed || d.RetryAfterMs < 3000 || d.RetryAfterMs > 20000 {
			t.Fatal("unregistered reconnect escaped spread", d, e)
		}
		delays[d.RetryAfterMs] = true
	}
	if len(delays) < 10 {
		t.Fatal("synchronized reconnect schedules")
	}
	g.client.Do(ctx, g.client.B().Hset().Key(g.keys[2]).FieldValue().FieldValue("clock", fmt.Sprint(time.Now().Add(time.Hour).UnixMilli())).Build())
	before, _ := g.client.Do(ctx, g.client.B().Hgetall().Key(g.keys[0]).Build()).AsStrMap()
	if _, e := g.Check(ctx, source, "join", hashed("clock")); e != ErrUnavailable {
		t.Fatal("clock rollback allowed", e)
	}
	after, _ := g.client.Do(ctx, g.client.B().Hgetall().Key(g.keys[0]).Build()).AsStrMap()
	if len(before) != len(after) {
		t.Fatal("clock failure changed quotas")
	}
}

func TestMetadataCapacityPreservesExistingTicketAndReplay(t *testing.T) {
	g, _ := fixture(t)
	ctx := context.Background()
	source, ticket, key := hashed("source"), hashed("ticket"), hashed("key")
	now, e := g.client.Do(ctx, g.client.B().Time().Build()).ToArray()
	if e != nil {
		t.Fatal(e)
	}
	sec, _ := now[0].AsInt64()
	if sec%60 > 50 {
		time.Sleep(time.Duration(61-sec%60) * time.Second)
	}
	for _, op := range []struct{ op, id string }{{"register", ticket}, {"join", key}, {"heartbeat", ticket}} {
		if d, e := g.Check(ctx, source, op.op, op.id); e != nil || !d.Allowed {
			t.Fatal("seed", op, e)
		}
	}
	count, e := g.client.Do(ctx, g.client.B().Hlen().Key(g.keys[0]).Build()).AsInt64()
	if e != nil {
		t.Fatal(e)
	}
	// Populate inert metadata in this test's isolated namespace, without creating
	// queue visitors. Both indexes must stay synchronized at the exact budget.
	for n := count; n < 42048; {
		h := g.client.B().Hset().Key(g.keys[0]).FieldValue()
		z := g.client.B().Zadd().Key(g.keys[1]).ScoreMember()
		for j := 0; j < 512 && n < 42048; j++ {
			id := fmt.Sprint("fixture-", n)
			h = h.FieldValue(id, "1")
			z = z.ScoreMember(float64(time.Now().Add(time.Hour).UnixMilli()), id)
			n++
		}
		for _, out := range g.client.DoMulti(ctx, h.Build(), z.Build()) {
			if e := out.Error(); e != nil {
				t.Fatal(e)
			}
		}
	}
	if d, e := g.Check(ctx, source, "join", hashed("new")); e != nil || d.Allowed {
		t.Fatal("new metadata exceeded cap", d, e)
	}
	for _, op := range []struct{ op, id string }{{"register", ticket}, {"join", key}, {"heartbeat", ticket}, {"claim", ticket}} {
		if d, e := g.Check(ctx, source, op.op, op.id); e != nil || !d.Allowed {
			t.Fatal("full metadata blocked existing credential", op, d, e)
		}
	}
	if d, e := g.Check(ctx, source, "status", ticket); e != nil || d.Allowed || d.RetryAfterMs <= 0 {
		t.Fatal("full metadata lost existing poll schedule", d, e)
	}
	a, _ := g.client.Do(ctx, g.client.B().Hlen().Key(g.keys[0]).Build()).AsInt64()
	b, _ := g.client.Do(ctx, g.client.B().Zcard().Key(g.keys[1]).Build()).AsInt64()
	if a != 42048 || b != a {
		t.Fatal("metadata cap changed", a, b)
	}
}

func TestKnownJoinReplayUsesExistingRequestQuota(t *testing.T) {
	g, _ := fixture(t)
	ctx := context.Background()
	source, key := hashed("source"), hashed("repeat")
	now, e := g.client.Do(ctx, g.client.B().Time().Build()).ToArray()
	if e != nil {
		t.Fatal(e)
	}
	sec, _ := now[0].AsInt64()
	if sec%60 > 45 {
		time.Sleep(time.Duration(61-sec%60) * time.Second)
	}
	if d, e := g.Check(ctx, source, "join", key); e != nil || !d.Allowed {
		t.Fatal(e)
	}
	for i := 0; i < 6000; i++ {
		if d, e := g.Check(ctx, source, "join", key); e != nil || !d.Allowed {
			t.Fatal("replay budget", i, e)
		}
	}
	if d, e := g.Check(ctx, source, "join", key); e != nil || d.Allowed {
		t.Fatal("replay abuse unbounded", d, e)
	}
	if d, e := g.Check(ctx, hashed("other-source"), "join", key); e != nil || !d.Allowed {
		t.Fatal("unrelated replay blocked", d, e)
	}
}
