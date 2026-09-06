// SPDX-License-Identifier: Apache-2.0
package adminserver

import "testing"

func TestApplicationUIRoutes(t *testing.T) {
	for _, path := range []string{"/auth/session", "/rooms", "/rooms/new", "/settings", "/dashboard/runtime", "/rooms/sale", "/rooms/sale/operations", "/rooms/sale/settings", "/rooms/sale/schedule", "/rooms/sale/verification"} {
		if !applicationUIRoute(path, false) {
			t.Errorf("valid route rejected: %s", path)
		}
		if applicationUIRoute(path, true) {
			t.Errorf("setup listener exposes application: %s", path)
		}
	}
	for _, path := range []string{"//example.test", "/rooms/%73ale", "/rooms/SALE", "/rooms/sale/unknown", "/rooms/sale/", "/rooms/sale?tab=settings", "/rooms/../settings", "/api/admin/v1/config/draft", "/assets/missing.js"} {
		if applicationUIRoute(path, false) {
			t.Errorf("invalid route accepted: %s", path)
		}
	}
}
