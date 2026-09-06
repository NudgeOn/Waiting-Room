// SPDX-License-Identifier: Apache-2.0
package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"waiting-room/internal/queue/model"
)

type Runtime struct {
	Revision   int64  `json:"revision"`
	Mode       string `json:"mode"`
	Limits     Limits `json:"limits"`
	Epoch      uint64 `json:"epoch"`
	EventState string `json:"eventState"`
}
type RoomRuntime struct {
	RoomID  string  `json:"roomId"`
	Runtime Runtime `json:"runtime"`
}

// Delivery is the entire signed desired state, never a partially applied patch.
type Delivery struct {
	Config   Config        `json:"config"`
	Runtimes []RoomRuntime `json:"runtimes"`
}
type RuntimeCommand struct {
	Action string  `json:"action"`
	Limits *Limits `json:"limits,omitempty"`
}

func (r Runtime) Validate(profile string) error {
	if r.Revision < 1 || r.Revision >= 9007199254740990 || r.Epoch < 1 || r.Epoch >= 9007199254740990 || r.Limits.Validate(profile) != nil {
		return ErrInvalid
	}
	switch r.Mode {
	case "OFF", "AUTO", "HOLD", "DRAINING", "RECOVERY_HOLD":
	default:
		return ErrInvalid
	}
	switch r.EventState {
	case "none", "scheduled", "running", "paused_by_override":
	default:
		return ErrInvalid
	}
	return nil
}
func (d Delivery) Validate() error {
	if d.Config.Validate() != nil || len(d.Runtimes) != len(d.Config.Rooms) {
		return ErrInvalid
	}
	for i, r := range d.Config.Rooms {
		v := d.Runtimes[i]
		if v.RoomID != r.ID || v.Runtime.Validate(d.Config.Profile) != nil || (!r.Active && v.Runtime.Mode != "OFF") {
			return ErrInvalid
		}
	}
	return nil
}
func (d Delivery) Bytes() []byte { b, _ := json.Marshal(d); return b }
func ValidateDelivery(raw json.RawMessage) error {
	var d Delivery
	if DecodeExact(raw, &d) != nil || d.Validate() != nil || !bytes.Equal(d.Bytes(), raw) {
		return ErrInvalid
	}
	return nil
}
func (d Delivery) Find(id string) (Room, Runtime, bool) {
	for i, r := range d.Config.Rooms {
		if r.ID == id {
			return r, d.Runtimes[i].Runtime, true
		}
	}
	return Room{}, Runtime{}, false
}
func InitialRuntime(room Room) Runtime {
	mode := "OFF"
	if room.Active {
		mode = "HOLD"
	}
	return Runtime{Revision: 1, Mode: mode, Limits: room.Limits, Epoch: 1, EventState: "none"}
}
func (r Runtime) ETag() string { return fmt.Sprintf(`"runtime-%d"`, r.Revision) }
func (r Runtime) QueueConfig(profile string, room Room) model.Config {
	c := model.DefaultConfig()
	if profile == "high-scale-100k" {
		c.VisitorCap = 100000
		c.IdempotencyCap = 200000
	}
	c.LeaseCap = r.Limits.MaxActiveAdmissionLeases
	c.Rate = r.Limits.AdmissionsPerMinute
	c.AdmissionTTL = int64(r.Limits.AdmissionTTLSeconds) * 1000
	c.IdleTTL = int64(room.QueuePolicy.TicketIdleTTLSeconds) * 1000
	c.TicketTTL = int64(room.QueuePolicy.TicketMaxTTLSeconds) * 1000
	c.ReadyTTL = int64(room.QueuePolicy.ReadyTTLSeconds) * 1000
	return c
}

type EventInput struct {
	PrequeueAt time.Time `json:"prequeueAt"`
	AdmitAt    time.Time `json:"admitAt"`
	DrainAt    time.Time `json:"drainAt"`
}
type Event struct {
	ID         string    `json:"id"`
	RoomID     string    `json:"roomId"`
	PrequeueAt time.Time `json:"prequeueAt"`
	AdmitAt    time.Time `json:"admitAt"`
	DrainAt    time.Time `json:"drainAt"`
	State      string    `json:"state"`
}

func (e EventInput) Validate(now time.Time) error {
	if e.PrequeueAt.Before(now) || !e.PrequeueAt.Before(e.AdmitAt) || !e.AdmitAt.Before(e.DrainAt) || e.DrainAt.After(now.Add(366*24*time.Hour)) {
		return ErrInvalid
	}
	return nil
}
func (e Event) Overlaps(other EventInput) bool {
	return e.State != "cancelled" && e.State != "completed" && e.PrequeueAt.Before(other.DrainAt) && other.PrequeueAt.Before(e.DrainAt)
}

// NextEventMode never advances a paused manual override or a cancelled event.
// Time catch-up chooses the latest due stage, not three stale intermediate writes.
func NextEventMode(e Event, now time.Time) (mode, state string) {
	if e.State == "paused_by_override" || e.State == "cancelled" || e.State == "completed" || now.Before(e.PrequeueAt) {
		return "", e.State
	}
	if !now.Before(e.DrainAt) {
		return "DRAINING", "running"
	}
	if !now.Before(e.AdmitAt) {
		return "AUTO", "running"
	}
	return "HOLD", "running"
}
