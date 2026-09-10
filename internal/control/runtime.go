// SPDX-License-Identifier: Apache-2.0
package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"waiting-room/internal/configtrust"
	"waiting-room/internal/queue/model"
)

type Runtime struct {
	Revision      int64  `json:"revision"`
	Mode          string `json:"mode"`
	Limits        Limits `json:"limits"`
	Epoch         uint64 `json:"epoch"`
	EventState    string `json:"eventState"`
	RecoveryUntil int64  `json:"recoveryUntil,omitempty"`
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

// FitsDeliveryBudget reserves the largest permitted runtime and envelope fields,
// so a saved draft can still be signed after later mode/epoch/revision changes.
// The existing 64 KiB wire/storage ceiling is not raised for uploaded logos.
func (c Config) FitsDeliveryBudget() bool {
	c.Revision = 9007199254740989
	d := Delivery{Config: c, Runtimes: []RoomRuntime{}}
	for _, room := range c.Rooms {
		d.Runtimes = append(d.Runtimes, RoomRuntime{RoomID: room.ID, Runtime: Runtime{Revision: 9007199254740989, Mode: "RECOVERY_HOLD", Limits: Limits{100000, 60000, 3600}, Epoch: 9007199254740989, EventState: "paused_by_override", RecoveryUntil: 9007199254740989}})
	}
	snapshot, _ := json.Marshal(configtrust.Snapshot{SchemaVersion: 1, Installation: strings.Repeat("i", 80), Generation: 9007199254740991, Revision: uint64(c.Revision), IssuedAt: 253402127999, ExpiresAt: 253402214399, Kid: strings.Repeat("k", 80), Payload: d.Bytes()})
	envelope, _ := json.Marshal(struct {
		Snapshot  json.RawMessage `json:"snapshot"`
		Signature string          `json:"signature"`
	}{snapshot, strings.Repeat("s", 86)})
	return len(d.Bytes()) <= MaxConfigBytes && len(envelope) <= configtrust.MaxSnapshotBytes
}

type RuntimeCommand struct {
	Action     string  `json:"action"`
	Limits     *Limits `json:"limits,omitempty"`
	Scope      string  `json:"scope,omitempty"`
	Generation *int64  `json:"generation,omitempty"`
}

func (r Runtime) Validate(profile string) error {
	if r.RecoveryUntil < 0 || r.RecoveryUntil >= 9007199254740990 || r.Revision < 1 || r.Revision >= 9007199254740990 || r.Epoch < 1 || r.Epoch >= 9007199254740990 || r.Limits.Validate(profile) != nil {
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
