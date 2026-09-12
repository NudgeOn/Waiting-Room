// SPDX-License-Identifier: Apache-2.0
package valkeystore

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
	"time"

	"waiting-room/internal/queue/model"
)

type WaitEstimate struct {
	Min int64 `json:"min"`
	Max int64 `json:"max"`
}

// Progress is an approximate, read-only observation. It never grants admission.
type Progress struct {
	UsersAhead int64
	Mode       string
	Estimate   *WaitEstimate
}

// StatusWithProgress preserves the authoritative status even if optional hints
// time out. Sorted-set reads are bounded; no ticket scan, TTL refresh, new Lua
// ABI or persistent schema change is needed. Concurrent promotions/expiry may
// make the rank approximate until the next poll.
func (s *Store) StatusWithProgress(ctx context.Context, id string) (Result, error) {
	r, err := s.Status(ctx, id)
	if err != nil || r.Ticket == nil || r.Ticket.State != model.Waiting {
		return r, err
	}
	hints, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	b := s.client.B()
	values := s.client.DoMulti(hints,
		b.Zrank().Key(s.keys[2]).Member(id).Build(),
		b.Hget().Key(s.keys[0]).Field("mode").Build(),
		b.Hget().Key(s.keys[0]).Field("config").Build(),
		b.Zcount().Key(s.keys[5]).Min("("+strconv.FormatInt(r.Now-60000, 10)).Max("+inf").Build(),
	)
	for _, value := range values {
		if value.Error() != nil {
			return r, nil
		}
	}
	ahead, e1 := values[0].AsInt64()
	mode, e2 := values[1].ToString()
	raw, e3 := values[2].ToString()
	rate, e4 := values[3].AsInt64()
	var c model.Config
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil || ahead < 0 || rate < 0 || json.Unmarshal([]byte(raw), &c) != nil {
		return r, nil
	}
	if _, err := model.New(c); err != nil {
		return r, nil
	}
	if ahead >= int64(c.VisitorCap) || (mode != "HOLD" && mode != "AUTO" && mode != "DRAINING" && mode != "OFF") {
		return r, nil
	}
	r.Progress = &Progress{UsersAhead: ahead, Mode: mode, Estimate: estimateWait(ahead, rate, c, mode)}
	return r, nil
}

func estimateWait(ahead, observedRate int64, c model.Config, mode string) *WaitEstimate {
	if (mode != "AUTO" && mode != "DRAINING") || observedRate <= 0 {
		return nil
	}
	// Recent promotions include reserved READY slots. Account for both the
	// configured rate and lease lifetime; the range is guidance, never an SLA.
	perMinute := math.Min(float64(observedRate), float64(c.Rate))
	perMinute = math.Min(perMinute, float64(c.LeaseCap)*60000/float64(c.AdmissionTTL+c.ClockSkew))
	if perMinute <= 0 {
		return nil
	}
	seconds := float64(ahead+1) * 60 / perMinute
	return &WaitEstimate{Min: max(1, int64(math.Floor(seconds*.75))), Max: max(3, int64(math.Ceil(seconds*1.5)))}
}
