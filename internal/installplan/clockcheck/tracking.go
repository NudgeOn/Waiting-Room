// SPDX-License-Identifier: Apache-2.0
package clockcheck

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

var referencePattern = regexp.MustCompile(`^[0-9A-F]{8}( \([!-~]{1,253}\))?$`)
var secondsPattern = regexp.MustCompile(`^[0-9]{1,5}(\.[0-9]{1,9})?$`)
var systemPattern = regexp.MustCompile(`^([0-9]{1,5}(?:\.[0-9]{1,9})?) seconds (fast|slow) of NTP time$`)

// Fail closed on unsupported output. The deliberately narrow English tracking
// format is described by chronyc(1); locale is pinned by the command adapter.
func parseTracking(raw []byte, now time.Time) (*Artifact, string) {
	bad := "CLOCK_OUTPUT_INVALID"
	if len(raw) == 0 || len(raw) > MaxOutputBytes || now.IsZero() {
		return nil, bad
	}
	for _, b := range raw {
		if (b < 32 && b != '\n') || b > 126 {
			return nil, bad
		}
	}
	keys := []string{"Reference ID", "Stratum", "Ref time (UTC)", "System time", "Last offset", "RMS offset", "Frequency", "Residual freq", "Skew", "Root delay", "Root dispersion", "Update interval", "Leap status"}
	fields := make(map[string]string, len(keys))
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		k, v, ok := strings.Cut(line, ":")
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if !ok || v == "" || fields[k] != "" {
			return nil, bad
		}
		fields[k] = v
	}
	if len(fields) != len(keys) {
		return nil, bad
	}
	for _, k := range keys {
		if fields[k] == "" {
			return nil, bad
		}
	}
	ref := fields["Reference ID"]
	if !referencePattern.MatchString(ref) {
		return nil, bad
	}
	stratum, err := strconv.Atoi(fields["Stratum"])
	if err != nil || stratum < 0 || stratum > 16 || strconv.Itoa(stratum) != fields["Stratum"] {
		return nil, bad
	}
	offset := systemPattern.FindStringSubmatch(fields["System time"])
	if offset == nil {
		return nil, bad
	}
	d, ok := seconds(offset[1])
	if !ok {
		return nil, bad
	}
	if offset[2] == "slow" {
		d = -d
	}
	delay, ok := seconds(strings.TrimSuffix(fields["Root delay"], " seconds"))
	if !ok || !strings.HasSuffix(fields["Root delay"], " seconds") {
		return nil, bad
	}
	dispersion, ok := seconds(strings.TrimSuffix(fields["Root dispersion"], " seconds"))
	if !ok || !strings.HasSuffix(fields["Root dispersion"], " seconds") {
		return nil, bad
	}
	leap := fields["Leap status"]
	if leap != "Normal" && leap != "Not synchronised" && leap != "Insert second" && leap != "Delete second" {
		return nil, bad
	}
	refAt, err := time.Parse("Mon Jan _2 15:04:05 2006", fields["Ref time (UTC)"])
	if err != nil {
		return nil, bad
	}
	a := &Artifact{SchemaVersion: 1, Method: "chrony", Source: "local-daemon-tracking-redacted", ReferenceAt: refAt, Stratum: stratum, OffsetNanos: int64(d), RootDelay: int64(delay), Dispersion: int64(dispersion), Synchronized: leap == "Normal" && stratum > 0 && stratum < 16 && !strings.HasPrefix(ref, "00000000")}
	if strings.HasPrefix(ref, "7F7F0101") {
		return a, "CLOCK_LOCAL_REFERENCE_UNVERIFIED"
	}
	if leap == "Insert second" || leap == "Delete second" {
		return a, "CLOCK_LEAP_EVENT_UNVERIFIED"
	}
	// Known unsynchronization or >5s system error is a policy failure even if
	// the last update is stale. Never hide a known failure behind a fresh check.
	if !a.Synchronized || absDuration(d) > 5*time.Second {
		return a, "CLOCK_OBSERVED"
	}
	// Conservative initial collector limits, not chrony defaults or proof of
	// UTC accuracy. Account for reported system offset when dating the sample.
	if now.Sub(refAt) > MaxReferenceAge+absDuration(d) || refAt.Sub(now) > 5*time.Second+absDuration(d) {
		return a, "CLOCK_REFERENCE_STALE_OR_FUTURE"
	}
	if absDuration(d)+dispersion+(delay+1)/2 > 5*time.Second {
		return a, "CLOCK_UNCERTAINTY_UNVERIFIED"
	}
	return a, "CLOCK_OBSERVED"
}

func seconds(s string) (time.Duration, bool) {
	if !secondsPattern.MatchString(s) {
		return 0, false
	}
	d, err := time.ParseDuration(s + "s")
	return d, err == nil
}
