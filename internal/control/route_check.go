// SPDX-License-Identifier: Apache-2.0
package control

import (
	"net/url"
	"strconv"
	"strings"
)

const MaxRouteURLBytes = 4096

// CheckRoute is a pure configuration diagnostic. It never resolves DNS, dials an
// origin, checks a listener port or grants admission. Queries do not affect rules.
// No URL text is reflected in the result, which can otherwise contain secrets.
func (c Config) CheckRoute(raw string) Match {
	invalid := Match{Decision: "invalid", Reason: "noncanonical_url"}
	if len(raw) == 0 || len(raw) > MaxRouteURLBytes || strings.ContainsAny(raw, "#\\") {
		return invalid
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Opaque != "" || !ValidHostname(u.Hostname()) || u.Host == "" {
		return invalid
	}
	// Reject whitespace/control bytes even in the query; preserve encoded paths
	// for MatchURL rather than normalizing potentially dangerous input in JS.
	for _, b := range []byte(raw) {
		if b <= 32 || b >= 127 {
			return invalid
		}
	}
	if port := u.Port(); port != "" {
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
			return invalid
		}
	} else if strings.Contains(u.Host, ":") {
		return invalid
	}
	if u.Path == "" {
		u.Path = "/"
	}
	return c.MatchURL(u.Hostname(), u)
}
