// SPDX-License-Identifier: Apache-2.0
// Package policy chooses eligible tickets; it never grants admission or capacity.
package policy

import (
	"errors"
	"sort"
)

var ErrUnsupported = errors.New("unsupported queue policy")

type Candidate struct {
	ID        string
	Sequence  uint64
	ExpiresAt int64
}

type Policy interface {
	Eligible(candidates []Candidate, now int64, limit int) []Candidate
}

type Capability struct {
	Kind          string `json:"kind"`
	SchemaVersion int    `json:"schemaVersion"`
}

func Capabilities() []Capability { return []Capability{{Kind: "fifo", SchemaVersion: 1}} }

func New(kind string) (Policy, error) {
	if kind != "fifo" {
		return nil, ErrUnsupported
	}
	return FIFO{}, nil
}

type FIFO struct{}

func (FIFO) Eligible(candidates []Candidate, now int64, limit int) []Candidate {
	if limit <= 0 {
		return nil
	}
	out := make([]Candidate, 0, len(candidates))
	for _, c := range candidates {
		if c.ExpiresAt > now {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Sequence == out[j].Sequence {
			return out[i].ID < out[j].ID
		}
		return out[i].Sequence < out[j].Sequence
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}
