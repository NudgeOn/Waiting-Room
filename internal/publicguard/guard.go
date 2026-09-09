// SPDX-License-Identifier: Apache-2.0
// Package publicguard shares bounded abuse counters and poll schedules across Coordinators.
// It never reads queue records and never stores source addresses or bearer tokens.
package publicguard

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	v "github.com/valkey-io/valkey-go"
	"regexp"
	"strings"
)

// ABI v2 keeps schema 1 and its existing counters/schedules. Older installations
// can retain their immutable v1 library while the owner installs v2 explicitly.
//
//go:embed guard_v2.lua
var library string
var ErrUnavailable = errors.New("public request guard unavailable")

type Decision struct {
	Allowed      bool  `json:"allowed"`
	Now          int64 `json:"now"`
	RetryAfterMs int64 `json:"retryAfterMs"`
	PollAfterMs  int64 `json:"pollAfterMs"`
}
type CheckFunc func(context.Context, string, string, string) (Decision, error)
type Guard struct {
	client           v.Client
	keys             []string
	room, epoch, cap string
}

func Install(ctx context.Context, c v.Client) error {
	if e := c.Do(ctx, c.B().FunctionLoad().FunctionCode(library).Build()).Error(); e != nil && !strings.Contains(e.Error(), "already exists") {
		return ErrUnavailable
	}
	return verify(ctx, c)
}
func verify(ctx context.Context, c v.Client) error {
	rows, e := c.Do(ctx, c.B().FunctionList().Libraryname("wr_public_guard_v2").Withcode().Build()).ToArray()
	if e != nil || len(rows) != 1 {
		return ErrUnavailable
	}
	m, e := rows[0].AsMap()
	if e != nil {
		return ErrUnavailable
	}
	x := m["library_code"]
	s, e := x.ToString()
	if e != nil || s != library {
		return ErrUnavailable
	}
	return nil
}
func Open(ctx context.Context, opt v.ClientOption, namespace, room string, epoch uint64, cap int) (*Guard, error) {
	if !regexp.MustCompile(`^wr:(runtime|lab):[a-zA-Z0-9_-]{1,80}$`).MatchString(namespace) || !regexp.MustCompile(`^[a-z2-7]{20}$`).MatchString(room) || epoch < 1 || epoch >= 9007199254740990 || (cap != 10000 && cap != 100000) {
		return nil, ErrUnavailable
	}
	opt.DisableCache = true
	opt.DisableRetry = true
	opt.ForceSingleClient = true
	c, e := v.NewClient(opt)
	if e != nil {
		return nil, ErrUnavailable
	}
	if e = verify(ctx, c); e != nil {
		c.Close()
		return nil, e
	}
	prefix := namespace + "{public-guard:1}:"
	return &Guard{c, []string{prefix + "records", prefix + "expiry", prefix + "meta"}, room, fmt.Sprint(epoch), fmt.Sprint(cap)}, nil
}
func (g *Guard) Close() { g.client.Close() }
func (g *Guard) Check(ctx context.Context, source, op, credential string) (Decision, error) {
	var d Decision
	raw, e := g.client.Do(ctx, g.client.B().Fcall().Function("wr_pg2_check").Numkeys(3).Key(g.keys...).Arg(g.room, g.epoch, source, op, credential, g.cap).Build()).ToString()
	if e != nil || json.Unmarshal([]byte(raw), &d) != nil || d.Now <= 0 || d.RetryAfterMs < 0 || d.RetryAfterMs > 60000 || d.PollAfterMs < 3000 || d.PollAfterMs > 20000 {
		return d, ErrUnavailable
	}
	return d, nil
}
