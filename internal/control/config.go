// SPDX-License-Identifier: Apache-2.0
// Package control owns the typed, non-secret operator configuration contract.
package control

import (
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	ErrInvalid  = errors.New("invalid configuration")
	ErrConflict = errors.New("route conflict")
)

type Limits struct {
	MaxActiveAdmissionLeases int `json:"maxActiveAdmissionLeases"`
	AdmissionsPerMinute      int `json:"admissionsPerMinute"`
	AdmissionTTLSeconds      int `json:"admissionTtlSeconds"`
}
type QueuePolicy struct {
	Kind                 string `json:"kind"`
	TicketIdleTTLSeconds int    `json:"ticketIdleTtlSeconds"`
	TicketMaxTTLSeconds  int    `json:"ticketMaxTtlSeconds"`
	ReadyTTLSeconds      int    `json:"readyTtlSeconds"`
}
type Theme struct {
	TemplateID        string `json:"templateId"`
	Title             string `json:"title"`
	Message           string `json:"message"`
	PrimaryColor      string `json:"primaryColor"`
	Locale            string `json:"locale"`
	ShowEstimatedWait bool   `json:"showEstimatedWait"`
	LogoImage         string `json:"logoImage,omitempty"`
}
type Room struct {
	ID              string      `json:"id"`
	PublicID        string      `json:"publicId"`
	Name            string      `json:"name"`
	Hostname        string      `json:"hostname"`
	Origin          string      `json:"origin"`
	HealthURL       string      `json:"healthURL"`
	ProtectPrefixes []string    `json:"protectPrefixes"`
	ExcludePrefixes []string    `json:"excludePrefixes"`
	QueuePolicy     QueuePolicy `json:"queuePolicy"`
	Limits          Limits      `json:"limits"`
	Theme           Theme       `json:"theme"`
	Active          bool        `json:"active"`
}
type Config struct {
	SchemaVersion int    `json:"schemaVersion"`
	Revision      int64  `json:"revision"`
	Profile       string `json:"profile"`
	RegionID      string `json:"regionId"`
	Rooms         []Room `json:"rooms"`
}

var IDPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
var publicIDPattern = regexp.MustCompile(`^[a-z2-7]{20}$`)
var regionPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
var colorPattern = regexp.MustCompile(`^#[a-fA-F0-9]{6}$`)

func (l Limits) Validate(profile string) error {
	cap, rate := 10000, 6000
	if profile == "high-scale-100k" {
		cap, rate = 100000, 60000
	} else if profile != "standard-10k" {
		return ErrInvalid
	}
	if l.MaxActiveAdmissionLeases < 1 || l.MaxActiveAdmissionLeases > cap || l.AdmissionsPerMinute < 1 || l.AdmissionsPerMinute > rate || l.AdmissionTTLSeconds < 60 || l.AdmissionTTLSeconds > 3600 {
		return ErrInvalid
	}
	return nil
}
func plain(s string, max int, multiline bool) bool {
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > max {
		return false
	}
	for _, c := range s {
		if c == '<' || c == '>' || (unicode.IsControl(c) && !(multiline && (c == '\n' || c == '\t'))) {
			return false
		}
	}
	return true
}
func ValidHostname(host string) bool {
	if len(host) == 0 || len(host) > 253 || host != strings.ToLower(host) {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

// Prefixes are decoded canonical ASCII paths. Request queries never affect matching.
func ValidPath(p string) bool {
	if p == "" || len(p) > 2048 || p[0] != '/' || strings.Contains(p, "//") {
		return false
	}
	for _, c := range p {
		if c <= 32 || c >= 127 || strings.ContainsRune("%\\?#;", c) {
			return false
		}
	}
	for _, segment := range strings.Split(p, "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	return true
}
func PrefixMatches(prefix, path string) bool {
	prefix = strings.TrimSuffix(prefix, "/")
	return prefix == "" || path == prefix || strings.HasPrefix(path, prefix+"/")
}
func reserved(p string) bool {
	return PrefixMatches("/_wr", p) || PrefixMatches("/api/admin", p)
}
func validOrigin(raw string) (*url.URL, bool) {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.User != nil || !ValidHostname(u.Hostname()) || u.Fragment != "" || u.RawQuery != "" || u.ForceQuery || u.Opaque != "" || u.RawPath != "" {
		return nil, false
	}
	if u.Port() != "" {
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 || strconv.Itoa(port) != u.Port() {
			return nil, false
		}
	}
	return u, true
}
func (r Room) Validate(profile string) error {
	if !IDPattern.MatchString(r.ID) || !publicIDPattern.MatchString(r.PublicID) || strings.TrimSpace(r.Name) == "" || !plain(r.Name, 120, false) || !ValidHostname(r.Hostname) {
		return ErrInvalid
	}
	origin, ok := validOrigin(r.Origin)
	if !ok || (origin.Path != "" && origin.Path != "/") || origin.Hostname() == r.Hostname {
		return ErrInvalid
	}
	health, ok := validOrigin(r.HealthURL)
	if !ok || health.Host != origin.Host || !ValidPath(health.Path) {
		return ErrInvalid
	}
	if len(r.ProtectPrefixes) == 0 || len(r.ProtectPrefixes) > 32 || r.ExcludePrefixes == nil || len(r.ExcludePrefixes) > 32 {
		return ErrInvalid
	}
	for _, prefixes := range [][]string{r.ProtectPrefixes, r.ExcludePrefixes} {
		seen := map[string]bool{}
		for _, p := range prefixes {
			canonical := strings.TrimSuffix(p, "/")
			if !ValidPath(p) || reserved(p) || seen[canonical] {
				return ErrInvalid
			}
			seen[canonical] = true
		}
	}
	p := r.QueuePolicy
	if p.Kind != "fifo" || p.TicketIdleTTLSeconds < 60 || p.TicketIdleTTLSeconds > 600 || p.TicketMaxTTLSeconds < 600 || p.TicketMaxTTLSeconds > 86400 || p.TicketMaxTTLSeconds < p.TicketIdleTTLSeconds || p.ReadyTTLSeconds < 1 || p.ReadyTTLSeconds > 120 {
		return ErrInvalid
	}
	if r.Limits.Validate(profile) != nil {
		return ErrInvalid
	}
	t := r.Theme
	if t.TemplateID != "calm" || !plain(t.Title, 120, false) || !plain(t.Message, 1000, true) || !colorPattern.MatchString(t.PrimaryColor) || (t.Locale != "ko" && t.Locale != "en") {
		return ErrInvalid
	}
	if _, err := LogoBytes(t.LogoImage); err != nil {
		return ErrInvalid
	}
	return nil
}
func (c Config) Validate() error {
	if c.SchemaVersion != 1 || c.Revision < 0 || c.Revision >= 9007199254740990 || !regionPattern.MatchString(c.RegionID) || c.Rooms == nil || len(c.Rooms) > 100 || (c.Profile != "standard-10k" && c.Profile != "high-scale-100k") {
		return ErrInvalid
	}
	ids, publicIDs := map[string]bool{}, map[string]bool{}
	for i, r := range c.Rooms {
		if r.Validate(c.Profile) != nil || ids[r.ID] || publicIDs[r.PublicID] {
			return ErrInvalid
		}
		ids[r.ID] = true
		publicIDs[r.PublicID] = true
		if !r.Active {
			continue
		}
		for _, other := range c.Rooms[:i] {
			if !other.Active || other.Hostname != r.Hostname {
				continue
			}
			for _, a := range r.ProtectPrefixes {
				for _, b := range other.ProtectPrefixes {
					if PrefixMatches(a, b) || PrefixMatches(b, a) {
						return ErrConflict
					}
				}
			}
		}
	}
	return nil
}

type Match struct {
	Decision string `json:"decision"`
	RoomID   string `json:"roomId,omitempty"`
	Reason   string `json:"reason"`
}

// MatchURL uses a trusted effective hostname, never Forwarded/X-Forwarded-Host.
// Encoded paths are conservatively rejected, including encoded unreserved bytes.
func (c Config) MatchURL(host string, u *url.URL) Match {
	if !ValidHostname(host) || u == nil || !ValidPath(u.Path) || u.RawPath != "" || u.Opaque != "" {
		return Match{Decision: "invalid", Reason: "noncanonical_url"}
	}
	if reserved(u.Path) {
		return Match{Decision: "reserved", Reason: "internal_namespace"}
	}
	for _, r := range c.Rooms {
		if !r.Active || r.Hostname != host {
			continue
		}
		for _, p := range r.ExcludePrefixes {
			if PrefixMatches(p, u.Path) {
				return Match{Decision: "excluded", RoomID: r.ID, Reason: "exclude_prefix"}
			}
		}
	}
	for _, r := range c.Rooms {
		if !r.Active || r.Hostname != host {
			continue
		}
		for _, p := range r.ProtectPrefixes {
			if PrefixMatches(p, u.Path) {
				return Match{Decision: "protected", RoomID: r.ID, Reason: "protect_prefix"}
			}
		}
	}
	return Match{Decision: "unprotected", Reason: "no_active_rule"}
}
func (c Config) Bytes() []byte { b, _ := json.Marshal(c); return b }
