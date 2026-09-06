// SPDX-License-Identifier: Apache-2.0
// Package model is a deterministic, single-threaded test oracle, not a production store.
// All times are milliseconds. Expired tickets are retained for trace inspection.
package model

import (
	"errors"
	"fmt"
	"sort"

	"waiting-room/internal/policy"
)

const Minute int64 = 60_000

type State string

const (
	Waiting  State = "WAITING"
	Ready    State = "READY"
	Admitted State = "ADMITTED"
	Expired  State = "EXPIRED"
)

type Mode string

const (
	Auto     Mode = "AUTO"
	Hold     Mode = "HOLD"
	Draining Mode = "DRAINING"
	Recovery Mode = "RECOVERY_HOLD"
)

var (
	ErrCapacity    = errors.New("visitor or idempotency capacity exceeded")
	ErrConflict    = errors.New("idempotency fingerprint conflict")
	ErrExpired     = errors.New("ticket expired")
	ErrNotReady    = errors.New("ticket not ready")
	ErrUnavailable = errors.New("queue unavailable")
	ErrDrain       = errors.New("queue draining")
	ErrClock       = errors.New("clock cannot move backwards")
)

type Config struct {
	VisitorCap, IdempotencyCap, LeaseCap, Rate                            int
	IdleTTL, TicketTTL, ReadyTTL, AdmissionTTL, IdempotencyTTL, ClockSkew int64
}

func DefaultConfig() Config {
	return Config{VisitorCap: 10_000, IdempotencyCap: 20_000, LeaseCap: 1000, Rate: 600,
		IdleTTL: 600_000, TicketTTL: 86_400_000, ReadyTTL: 120_000,
		AdmissionTTL: 900_000, IdempotencyTTL: 600_000, ClockSkew: 30_000}
}

type Ticket struct {
	ID                                                             string
	Sequence                                                       uint64
	State                                                          State
	Epoch                                                          uint64
	JoinedAt, IdleUntil, AbsoluteUntil, ReadyUntil, AdmissionUntil int64
	JTI                                                            string
	PromotedAt                                                     int64 `json:"PromotedAt,omitempty"`
}
type idem struct {
	ID, Fingerprint string
	Until           int64
}
type Promotion struct {
	ID       string
	Sequence uint64
	At       int64
	Epoch    uint64
}

type Model struct {
	config          Config
	now             int64
	epoch, sequence uint64
	mode            Mode
	unsafeUntil     int64
	cutoff          uint64
	tickets         map[string]Ticket
	idempotency     map[string]idem
	rateTimes       []int64
	promotions      []Promotion
}

func New(c Config) (*Model, error) {
	if c.VisitorCap <= 0 || c.IdempotencyCap <= 0 || c.LeaseCap <= 0 || c.LeaseCap > c.VisitorCap || c.Rate <= 0 ||
		c.IdleTTL <= 0 || c.TicketTTL < c.IdleTTL || c.ReadyTTL <= 0 || c.AdmissionTTL < Minute || c.AdmissionTTL > 60*Minute ||
		c.IdempotencyTTL <= 0 || c.ClockSkew < 0 || c.ClockSkew > 30_000 {
		return nil, errors.New("invalid model config")
	}
	return &Model{config: c, epoch: 1, mode: Auto, tickets: map[string]Ticket{}, idempotency: map[string]idem{}}, nil
}

func (m *Model) Advance(to int64) error {
	if to < m.now {
		return ErrClock
	}
	m.now = to
	for id, t := range m.tickets {
		switch t.State {
		case Waiting:
			if m.now >= min(t.IdleUntil, t.AbsoluteUntil) {
				t.State = Expired
			}
		case Ready:
			if m.now >= t.ReadyUntil {
				t.State = Expired
			}
		case Admitted:
			// Keep capacity until a verifier's maximum expiry leeway has elapsed.
			if m.now >= t.AdmissionUntil+m.config.ClockSkew {
				t.State = Expired
			}
		}
		m.tickets[id] = t
	}
	for key, value := range m.idempotency {
		if m.now >= value.Until {
			delete(m.idempotency, key)
		}
	}
	i := 0
	for i < len(m.rateTimes) && m.rateTimes[i] <= m.now-Minute {
		i++
	}
	m.rateTimes = m.rateTimes[i:]
	return nil
}

func (m *Model) Join(key, fingerprint string) (Ticket, error) {
	if key == "" || fingerprint == "" {
		return Ticket{}, errors.New("key and fingerprint required")
	}
	if old, ok := m.idempotency[key]; ok {
		if old.Fingerprint != fingerprint {
			return Ticket{}, ErrConflict
		}
		return m.tickets[old.ID], nil
	}
	if m.mode == Recovery {
		return Ticket{}, ErrUnavailable
	}
	if m.mode == Draining {
		return Ticket{}, ErrDrain
	}
	if m.VisitorCount() >= m.config.VisitorCap || len(m.idempotency) >= m.config.IdempotencyCap {
		return Ticket{}, ErrCapacity
	}
	m.sequence++
	id := fmt.Sprintf("%d:%d", m.epoch, m.sequence)
	t := Ticket{ID: id, Sequence: m.sequence, Epoch: m.epoch, State: Waiting,
		JoinedAt: m.now, IdleUntil: m.now + m.config.IdleTTL, AbsoluteUntil: m.now + m.config.TicketTTL}
	m.tickets[id] = t
	m.idempotency[key] = idem{id, fingerprint, m.now + m.config.IdempotencyTTL}
	return t, nil
}

// Status never refreshes idle expiry, promotes, claims, or mutates state.
func (m *Model) Status(id string) (Ticket, error) {
	t, ok := m.tickets[id]
	if !ok || t.State == Expired {
		return Ticket{}, ErrExpired
	}
	return t, nil
}

// Heartbeat is an explicit write, used by waiting clients before idle expiry.
func (m *Model) Heartbeat(id string) error {
	t, err := m.Status(id)
	if err != nil {
		return err
	}
	if m.mode == Recovery {
		return ErrUnavailable
	}
	if t.State == Waiting {
		t.IdleUntil = min(m.now+m.config.IdleTTL, t.AbsoluteUntil)
		m.tickets[id] = t
	}
	return nil
}

func (m *Model) Promote(limit int) []Ticket {
	if m.mode != Auto && m.mode != Draining {
		return nil
	}
	budget := min(limit, m.config.LeaseCap-m.LeaseCount(), m.config.Rate-len(m.rateTimes))
	if budget <= 0 {
		return nil
	}
	candidates := []policy.Candidate{}
	for _, t := range m.tickets {
		if t.State == Waiting && (m.mode != Draining || t.Sequence <= m.cutoff) {
			candidates = append(candidates, policy.Candidate{ID: t.ID, Sequence: t.Sequence, ExpiresAt: min(t.IdleUntil, t.AbsoluteUntil)})
		}
	}
	out := []Ticket{}
	for _, c := range (policy.FIFO{}).Eligible(candidates, m.now, budget) {
		t := m.tickets[c.ID]
		t.State = Ready
		t.AdmissionUntil = m.now + m.config.AdmissionTTL
		t.ReadyUntil = min(m.now+m.config.ReadyTTL, t.AdmissionUntil)
		t.JTI = fmt.Sprintf("admission:%s", t.ID)
		m.tickets[t.ID] = t
		m.rateTimes = append(m.rateTimes, m.now)
		m.promotions = append(m.promotions, Promotion{t.ID, t.Sequence, m.now, t.Epoch})
		out = append(out, t)
	}
	return out
}

func (m *Model) Claim(id string) (Ticket, error) {
	t, err := m.Status(id)
	if err != nil {
		return Ticket{}, err
	}
	if m.mode == Recovery {
		return Ticket{}, ErrUnavailable
	}
	if t.State == Admitted {
		if m.now >= t.AdmissionUntil {
			return Ticket{}, ErrExpired
		}
		return t, nil
	}
	if t.State != Ready {
		return Ticket{}, ErrNotReady
	}
	t.State = Admitted
	m.tickets[id] = t
	return t, nil
}

func (m *Model) SetHold(hold bool) error {
	if m.mode == Recovery {
		return ErrUnavailable
	}
	if hold {
		m.mode = Hold
	} else {
		m.mode = Auto
	}
	return nil
}
func (m *Model) Drain() error {
	if m.mode == Recovery {
		return ErrUnavailable
	}
	m.mode = Draining
	m.cutoff = m.sequence
	return nil
}
func (m *Model) Uncertain() int64 {
	m.mode = Recovery
	m.unsafeUntil = max(m.unsafeUntil, m.now+max(m.config.AdmissionTTL, m.config.ReadyTTL, Minute)+m.config.ClockSkew)
	return m.unsafeUntil
}
func (m *Model) Resume(verified bool) error {
	if m.mode != Recovery || !verified || m.now < m.unsafeUntil {
		return ErrUnavailable
	}
	m.mode = Auto
	return nil
}

// NewEpoch models explicit invalidation; real deployment must fence stale verifiers first.
func (m *Model) NewEpoch() {
	m.epoch++
	m.sequence = 0
	m.tickets = map[string]Ticket{}
	m.idempotency = map[string]idem{}
	m.rateTimes = nil
	m.unsafeUntil = 0
	m.mode = Hold
}
func (m *Model) VisitorCount() int {
	n := 0
	for _, t := range m.tickets {
		if t.State != Expired {
			n++
		}
	}
	return n
}
func (m *Model) LeaseCount() int {
	n := 0
	for _, t := range m.tickets {
		if t.State == Ready || t.State == Admitted {
			n++
		}
	}
	return n
}
func (m *Model) Snapshot() []Ticket {
	out := make([]Ticket, 0, len(m.tickets))
	for _, t := range m.tickets {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Sequence < out[j].Sequence })
	return out
}
func (m *Model) Promotions() []Promotion { return append([]Promotion(nil), m.promotions...) }
