// SPDX-License-Identifier: Apache-2.0
package control

import (
	"strings"
	"testing"
)

func TestCheckRouteUsesCanonicalRulesWithoutReflectingURL(t *testing.T) {
	c := fixture()
	for raw, want := range map[string]string{
		"https://shop.example.test/shop":                            "protected",
		"https://shop.example.test:20443/shop/cart?secret=redacted": "protected",
		"https://shop.example.test/shopping":                        "unprotected",
		"https://shop.example.test/shop/assets/logo.png":            "excluded",
		"https://shop.example.test/_wr/v1":                          "reserved",
		"https://shop.example.test/api/admin/v1":                    "reserved",
		"https://other.example.test/shop":                           "unprotected",
		"https://shop.example.test":                                 "unprotected",
	} {
		if got := c.CheckRoute(raw); got.Decision != want {
			t.Errorf("decision %s, want %s", got.Decision, want)
		}
	}
	for _, raw := range []string{"", "/shop", "//shop.example.test/shop", "http://shop.example.test/shop", "https://Shop.example.test/shop", "https://user:secret@shop.example.test/shop", "https://shop.example.test/shop#", "https://shop.example.test/shop#secret", "https://shop.example.test/shop%2fcart", "https://shop.example.test/%73hop", "https://shop.example.test/shop/../admin", "https://shop.example.test/shop//cart", "https://shop.example.test:/shop", "https://shop.example.test:0/shop", "https://shop.example.test:65536/shop", "https://shop.example.test:0443/shop", "https://shop.example.test/shop?x=raw space", "https://shop.example.test/shop\\cart", "https://shop.example.test/한글", strings.Repeat("a", MaxRouteURLBytes+1)} {
		if got := c.CheckRoute(raw); got.Decision != "invalid" || got.Reason != "noncanonical_url" || got.RoomID != "" {
			t.Errorf("unsafe URL accepted: %+v", got)
		}
	}
	c.Rooms[0].Active = false
	if c.CheckRoute("https://shop.example.test/shop").Decision != "unprotected" {
		t.Fatal("inactive draft treated as protected")
	}
}
