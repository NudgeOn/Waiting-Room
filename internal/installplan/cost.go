// SPDX-License-Identifier: Apache-2.0
package installplan

import (
	"encoding/json"
	"io"
	"math/big"
	"regexp"
	"strings"
	"time"
)

// CostInput uses currency units (not cents), exact decimal strings, and an
// explicitly chosen display precision. No exchange rates or price APIs exist.
type CostInput struct {
	SchemaVersion       int    `json:"schemaVersion"`
	Provider            string `json:"provider"`
	RegionID            string `json:"regionId"`
	Currency            string `json:"currency"`
	AsOf                string `json:"asOf"`
	FractionDigits      int    `json:"fractionDigits"`
	MonthlyHours        int    `json:"monthlyHours"`
	StandardHostHourly  string `json:"standardHostHourly"`
	HighWorkerHourly    string `json:"highWorkerHourly"`
	VolumeGiBMonthly    string `json:"volumeGiBMonthly"`
	HighWorkerVolumeGiB int    `json:"highWorkerVolumeGiB"`
	ExpectedEgressGiB   int    `json:"expectedEgressGiB"`
}

type CostLine struct {
	ID          string `json:"id"`
	Quantity    int    `json:"quantity"`
	Unit        string `json:"unit"`
	UnitPrice   string `json:"unitPrice"`
	Formula     string `json:"formula"`
	ExactAmount string `json:"exactAmount"`
}

type CostProfile struct {
	Profile          string     `json:"profile"`
	Hosts            int        `json:"hosts"`
	VCPUPerHost      int        `json:"vCPUPerHost"`
	MemoryGiBPerHost int        `json:"memoryGiBPerHost"`
	VolumeGiBPerHost int        `json:"volumeGiBPerHost"`
	Included         []CostLine `json:"included"`
	ExactSubtotal    string     `json:"exactSubtotal"`
	RoundedSubtotal  string     `json:"roundedSubtotal"`
	Excluded         []string   `json:"excluded"`
}

type CostReport struct {
	SchemaVersion       int           `json:"schemaVersion"`
	Scope               string        `json:"scope"`
	Input               CostInput     `json:"input"`
	Rounding            string        `json:"rounding"`
	Profiles            []CostProfile `json:"profiles"`
	HighToStandardRatio *string       `json:"highToStandardRatio"`
	RatioMeaning        string        `json:"ratioMeaning"`
	Warnings            []string      `json:"warnings"`
	Qualification       string        `json:"qualification"`
}

var pricePattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,8})(\.[0-9]{1,6})?$`)
var currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)

// Estimate compares provisioned host/worker plus volume subtotals. Pod resource
// requests are NOT added again to worker charges. External HA stores are NOT
// bundled into the High subtotal. It cannot be used as a total-cost quote.
func Estimate(in CostInput) (CostReport, error) {
	date, err := time.Parse("2006-01-02", in.AsOf)
	if err != nil || date.Year() < 2000 || date.Year() > 9999 || in.SchemaVersion != 1 ||
		!regionPattern.MatchString(in.Provider) || !regionPattern.MatchString(in.RegionID) || !currencyPattern.MatchString(in.Currency) ||
		in.FractionDigits < 0 || in.FractionDigits > 4 || in.MonthlyHours < 1 || in.MonthlyHours > 744 ||
		in.HighWorkerVolumeGiB < 1 || in.HighWorkerVolumeGiB > 65536 || in.ExpectedEgressGiB < 0 || in.ExpectedEgressGiB > 1000000000 ||
		!pricePattern.MatchString(in.StandardHostHourly) || !pricePattern.MatchString(in.HighWorkerHourly) || !pricePattern.MatchString(in.VolumeGiBMonthly) {
		return CostReport{}, ErrInput
	}
	s := costProfile("standard-10k", 1, 4, 8, 50, in.StandardHostHourly, in)
	h := costProfile("high-scale-100k", 3, 8, 16, in.HighWorkerVolumeGiB, in.HighWorkerHourly, in)
	r := CostReport{SchemaVersion: 1, Scope: "user-priced-reference-subtotals-only", Input: in,
		Rounding: "Exact integer micro-units (six decimal places); sum unrounded line amounts, then round subtotal half-up to fractionDigits. Ratio uses exact subtotals, rounded half-up to four decimals.",
		Profiles: []CostProfile{s, h}, Qualification: "NOT_RUN",
		RatioMeaning: "High/Standard ratio of INCLUDED subtotals only, using the same provider/region/currency/as-of inputs; not a total operating-cost ratio.",
		Warnings: []string{
			"User-entered prices, not provider-verified quotes. No pricing API, exchange-rate lookup or currency-registry validation is performed.",
			"Host/worker prices must cover the stated CPU and memory bundle but exclude separately priced volumes. Existing pod planning shares are not billed again.",
			"Standard includes its single host (including bundled stores) and 50 GiB host storage. High includes three workers and user-entered per-worker storage; external HA stores remain excluded.",
			"Monthly hours are an explicit compute billing assumption. Volumes are charged for a full month, without hourly proration. Discounts, tax, IOPS, snapshots and provider billing rules are not inferred.",
			"High worker volume size is a cost assumption, not a qualified storage recommendation. Pre-scaling pods does not automatically change this fixed worker-count estimate; extra nodes cost extra.",
			"Expected egress is recorded but unpriced. Missing excluded-item prices do not mean those items are free. This report does not certify installability, HA or 10K/100K capacity.",
		},
	}
	den := micro(s.ExactSubtotal)
	num := micro(h.ExactSubtotal)
	if den.Sign() == 0 {
		r.RatioMeaning = "UNAVAILABLE_ZERO_STANDARD_SUBTOTAL: a zero user-priced included subtotal does not mean operation is free."
	} else {
		scaled := new(big.Int).Mul(num, big.NewInt(10000))
		q := roundQuotient(scaled, den)
		text := fixed(q, 4)
		r.HighToStandardRatio = &text
	}
	return r, nil
}

func costProfile(profile string, hosts, cpu, memory, volume int, hourly string, in CostInput) CostProfile {
	computeQuantity := hosts * in.MonthlyHours
	volumeQuantity := hosts * volume
	compute := new(big.Int).Mul(micro(hourly), big.NewInt(int64(computeQuantity)))
	storage := new(big.Int).Mul(micro(in.VolumeGiBMonthly), big.NewInt(int64(volumeQuantity)))
	total := new(big.Int).Add(compute, storage)
	p := CostProfile{Profile: profile, Hosts: hosts, VCPUPerHost: cpu, MemoryGiBPerHost: memory, VolumeGiBPerHost: volume,
		Included: []CostLine{
			{ID: "compute", Quantity: computeQuantity, Unit: "host-hour", UnitPrice: hourly, Formula: "hosts * monthlyHours * hostHourly", ExactAmount: fixed(compute, 6)},
			{ID: "volume", Quantity: volumeQuantity, Unit: "GiB-month", UnitPrice: in.VolumeGiBMonthly, Formula: "hosts * volumeGiBPerHost * volumeGiBMonthly", ExactAmount: fixed(storage, 6)},
		}, ExactSubtotal: fixed(total, 6), RoundedSubtotal: fixed(roundQuotient(total, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(6-in.FractionDigits)), nil)), in.FractionDigits),
		Excluded: []string{"CDN/load-balancer", "egress", "backup-object-storage", "Prometheus-retention", "operator-labor", "tax", "IOPS-and-provider-additional-fees"},
	}
	if profile == "high-scale-100k" {
		p.Excluded = append(p.Excluded, "external-HA-stores-including-storage-and-sentinel", "Kubernetes-control-plane", "extra-workers-beyond-three")
	}
	return p
}

// micro is internal: callers validate raw decimals first; intermediate exact
// totals also have six fractional digits. big.Int prevents multiplication overflow.
func micro(s string) *big.Int {
	parts := strings.SplitN(s, ".", 2)
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	n, _ := new(big.Int).SetString(parts[0]+fraction+strings.Repeat("0", 6-len(fraction)), 10)
	return n
}

func fixed(n *big.Int, places int) string {
	s := n.String()
	if places == 0 {
		return s
	}
	if len(s) <= places {
		s = strings.Repeat("0", places-len(s)+1) + s
	}
	return s[:len(s)-places] + "." + s[len(s)-places:]
}

func roundQuotient(n, d *big.Int) *big.Int {
	q, r := new(big.Int), new(big.Int)
	q.QuoRem(n, d, r)
	if r.Mul(r, big.NewInt(2)).Cmp(d) >= 0 {
		q.Add(q, big.NewInt(1))
	}
	return q
}

func DecodeCost(r io.Reader) (CostInput, error) {
	b, root, err := decodeObject(r)
	if err != nil || !keys(root, "schemaVersion", "provider", "regionId", "currency", "asOf", "fractionDigits", "monthlyHours", "standardHostHourly", "highWorkerHourly", "volumeGiBMonthly", "highWorkerVolumeGiB", "expectedEgressGiB") {
		return CostInput{}, ErrInput
	}
	var in CostInput
	if json.Unmarshal(b, &in) != nil {
		return CostInput{}, ErrInput
	}
	if _, err := Estimate(in); err != nil {
		return CostInput{}, err
	}
	return in, nil
}
