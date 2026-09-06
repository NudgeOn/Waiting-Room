// SPDX-License-Identifier: Apache-2.0
package adminserver

import "regexp"

var roomUIRoute = regexp.MustCompile(`^/rooms/[a-z][a-z0-9_-]{0,63}(/(operations|settings|schedule|verification))?$`)

// UI routes return only the public shell. Every data request still authenticates.
// Setup listeners must not expose application routes or their APIs.
func applicationUIRoute(path string, setup bool) bool {
	if setup {
		return false
	}
	switch path {
	case "/auth/session", "/rooms", "/rooms/new", "/settings", "/dashboard/runtime":
		return true
	}
	return roomUIRoute.MatchString(path)
}
