// SPDX-License-Identifier: Apache-2.0
package installplan

import (
	"encoding/json"
	"math/big"
	"strings"
	"testing"
)

func costFixture(t *testing.T) CostInput {
	t.Helper()
	in, err := DecodeCost(strings.NewReader(string(fixture(t, "cost-example"))))
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func TestCostReferenceSubtotals(t *testing.T) {
	in := costFixture(t)
	r, err := Estimate(in)
	if err != nil {
		t.Fatal(err)
	}
	if r.Profiles[0].ExactSubtotal != "76.000000" || r.Profiles[1].ExactSubtotal != "888.000000" || *r.HighToStandardRatio != "11.6842" {
		t.Fatal(r)
	}
	if r.Input != in || r.Qualification != "NOT_RUN" {
		t.Fatal("provenance lost")
	}
	s, h := r.Profiles[0], r.Profiles[1]
	if s.Hosts != 1 || s.VCPUPerHost != 4 || s.MemoryGiBPerHost != 8 || s.VolumeGiBPerHost != 50 || s.Included[0].Quantity != 720 || s.Included[1].Quantity != 50 {
		t.Fatal(s)
	}
	if h.Hosts != 3 || h.VCPUPerHost != 8 || h.MemoryGiBPerHost != 16 || h.VolumeGiBPerHost != 100 || h.Included[0].Quantity != 2160 || h.Included[1].Quantity != 300 {
		t.Fatal(h)
	}
	if !strings.Contains(strings.Join(h.Excluded, ","), "external-HA-stores") || strings.Contains(strings.Join(s.Excluded, ","), "external-HA-stores") {
		t.Fatal("store double counting")
	}
}

func TestCostRoundingAndZero(t *testing.T) {
	in := costFixture(t)
	in.MonthlyHours = 1
	in.StandardHostHourly = "0.0049"
	in.VolumeGiBMonthly = "0.000002"
	r, _ := Estimate(in)
	if r.Profiles[0].ExactSubtotal != "0.005000" || r.Profiles[0].RoundedSubtotal != "0.01" {
		t.Fatal("must sum before rounding", r)
	}
	in.StandardHostHourly = "0.004899"
	r, _ = Estimate(in)
	if r.Profiles[0].RoundedSubtotal != "0.00" {
		t.Fatal("below half", r)
	}
	in.StandardHostHourly = "0.5"
	in.VolumeGiBMonthly = "0"
	in.FractionDigits = 0
	r, _ = Estimate(in)
	if r.Profiles[0].RoundedSubtotal != "1" {
		t.Fatal("whole currency half up")
	}
	in.FractionDigits = 4
	in.StandardHostHourly = "0.00005"
	r, _ = Estimate(in)
	if r.Profiles[0].RoundedSubtotal != "0.0001" {
		t.Fatal("4 place half up")
	}
	in.StandardHostHourly = "0"
	r, _ = Estimate(in)
	if r.HighToStandardRatio != nil || !strings.HasPrefix(r.RatioMeaning, "UNAVAILABLE_ZERO") {
		t.Fatal("zero division")
	}
	in.StandardHostHourly = "1"
	in.HighWorkerHourly = "0"
	r, _ = Estimate(in)
	if r.HighToStandardRatio == nil || *r.HighToStandardRatio != "0.0000" {
		t.Fatal("explicit zero not missing")
	}
}

func TestCostInvalidInputs(t *testing.T) {
	for name, change := range map[string]func(*CostInput){
		"version": func(i *CostInput) { i.SchemaVersion = 2 }, "provider": func(i *CostInput) { i.Provider = "https://user:DO-NOT-ECHO@host" },
		"region": func(i *CostInput) { i.RegionID = "a,b" }, "currency": func(i *CostInput) { i.Currency = "usd" },
		"date": func(i *CostInput) { i.AsOf = "2026-02-29" }, "date-year": func(i *CostInput) { i.AsOf = "1999-12-31" },
		"missing-date": func(i *CostInput) { i.AsOf = "" }, "precision-low": func(i *CostInput) { i.FractionDigits = -1 }, "precision-high": func(i *CostInput) { i.FractionDigits = 5 },
		"hours-low": func(i *CostInput) { i.MonthlyHours = 0 }, "hours-high": func(i *CostInput) { i.MonthlyHours = 745 },
		"storage-low": func(i *CostInput) { i.HighWorkerVolumeGiB = 0 }, "storage-high": func(i *CostInput) { i.HighWorkerVolumeGiB = 65537 },
		"egress-low": func(i *CostInput) { i.ExpectedEgressGiB = -1 }, "egress-high": func(i *CostInput) { i.ExpectedEgressGiB = 1000000001 },
	} {
		t.Run(name, func(t *testing.T) {
			in := costFixture(t)
			change(&in)
			if _, err := Estimate(in); err != ErrInput {
				t.Fatal("invalid value accepted")
			}
		})
	}
	for _, price := range []string{"", "-1", "+1", "1e3", "NaN", "Inf", "1.0000001", "01", ".1", "1.", "1000000000", "0\n", "DO-NOT-ECHO"} {
		for _, field := range []string{"host", "worker", "volume"} {
			t.Run(field+"/"+price, func(t *testing.T) {
				in := costFixture(t)
				switch field {
				case "host":
					in.StandardHostHourly = price
				case "worker":
					in.HighWorkerHourly = price
				case "volume":
					in.VolumeGiBMonthly = price
				}
				if _, err := Estimate(in); err != ErrInput {
					t.Fatal("invalid decimal accepted")
				}
			})
		}
	}
}

func TestCostStrictDecode(t *testing.T) {
	b := string(fixture(t, "cost-example"))
	for name, input := range map[string]string{
		"duplicate":    strings.Replace(b, `"fractionDigits": 2`, `"fractionDigits": 0, "fractionDigits": 2`, 1),
		"missing":      strings.Replace(b, `"fractionDigits": 2,`, "", 1),
		"null":         strings.Replace(b, `"fractionDigits": 2`, `"fractionDigits": null`, 1),
		"number-price": strings.Replace(b, `"standardHostHourly": "0.10"`, `"standardHostHourly": 0.10`, 1),
		"wrong-case":   strings.Replace(b, `"currency"`, `"Currency"`, 1),
		"secret":       strings.Replace(b, `"schemaVersion": 1`, `"password": "DO-NOT-ECHO", "schemaVersion": 1`, 1),
		"trailing":     b + b, "array": "[]", "oversized": b + strings.Repeat(" ", MaxInputBytes),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeCost(strings.NewReader(input)); err != ErrInput {
				t.Fatal("unsafe decode")
			}
		})
	}
}

func TestCostMaximumAndDeterminism(t *testing.T) {
	in := costFixture(t)
	in.StandardHostHourly = "999999999.999999"
	in.HighWorkerHourly = in.StandardHostHourly
	in.VolumeGiBMonthly = in.StandardHostHourly
	in.MonthlyHours = 744
	in.HighWorkerVolumeGiB = 65536
	r, err := Estimate(in)
	if err != nil {
		t.Fatal(err)
	}
	// Independent rational arithmetic: (3*744 + 3*65536) * price.
	price, _ := new(big.Rat).SetString(in.StandardHostHourly)
	expected := new(big.Rat).Mul(price, big.NewRat(198840, 1))
	if r.Profiles[1].ExactSubtotal != expected.FloatString(6) {
		t.Fatal("overflow", r.Profiles[1])
	}
	b, _ := json.Marshal(r)
	for n := 0; n < 50; n++ {
		t.Run("repeat", func(t *testing.T) {
			t.Parallel()
			r, _ := Estimate(in)
			got, _ := json.Marshal(r)
			if string(got) != string(b) {
				t.Fatal("not deterministic")
			}
			r.Profiles[0].Excluded[0] = "mutated"
		})
	}
}

func FuzzCostArithmetic(f *testing.F) {
	f.Add(uint32(100000), uint16(720), uint16(100))
	f.Fuzz(func(t *testing.T, raw uint32, hours, volume uint16) {
		price := fixed(new(big.Int).SetUint64(uint64(raw)), 6)
		in := CostInput{SchemaVersion: 1, Provider: "example", RegionID: "example", Currency: "USD", AsOf: "2026-09-05", FractionDigits: 2, MonthlyHours: int(hours)%744 + 1, StandardHostHourly: price, HighWorkerHourly: price, VolumeGiBMonthly: price, HighWorkerVolumeGiB: int(volume) + 1}
		r, err := Estimate(in)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range r.Profiles {
			q := int64(p.Hosts * (in.MonthlyHours + p.VolumeGiBPerHost))
			unit, _ := new(big.Rat).SetString(price)
			want := new(big.Rat).Mul(unit, big.NewRat(q, 1))
			if p.ExactSubtotal != want.FloatString(6) || p.RoundedSubtotal != want.FloatString(2) {
				t.Fatal("arithmetic mismatch")
			}
		}
	})
}
