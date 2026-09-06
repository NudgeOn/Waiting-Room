// SPDX-License-Identifier: Apache-2.0
package policy

import (
	"errors"
	"reflect"
	"testing"
)

func TestCapabilityRegistry(t *testing.T) {
	for _, kind := range []string{"lottery", "priority", "weighted", "", "FIFO"} {
		if _, err := New(kind); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("accepted %q", kind)
		}
	}
	c := Capabilities()
	c[0].Kind = "changed"
	if got := Capabilities(); len(got) != 1 || got[0].Kind != "fifo" {
		t.Fatal(got)
	}
	if _, err := New("fifo"); err != nil {
		t.Fatal(err)
	}
}

func TestFIFOEligibility(t *testing.T) {
	input := []Candidate{{"later", 3, 100}, {"expired", 1, 10}, {"first", 2, 100}}
	before := append([]Candidate(nil), input...)
	for _, limit := range []int{-1, 0, 1, 9} {
		got := (FIFO{}).Eligible(input, 10, limit)
		want := 2
		if limit <= 0 {
			want = 0
		} else if limit == 1 {
			want = 1
		}
		if len(got) != want {
			t.Fatalf("limit %d: %v", limit, got)
		}
		if want > 0 && got[0].ID != "first" {
			t.Fatal(got)
		}
	}
	if !reflect.DeepEqual(input, before) {
		t.Fatal("policy mutated caller input")
	}
}
