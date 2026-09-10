// SPDX-License-Identifier: Apache-2.0
package control

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

func fixture() Config {
	return Config{1, 0, "standard-10k", "local", []Room{{ID: "sale", PublicID: strings.Repeat("a", 20), Name: "판매", Hostname: "shop.example.test", Origin: "https://origin.example.test", HealthURL: "https://origin.example.test/health", ProtectPrefixes: []string{"/shop"}, ExcludePrefixes: []string{"/shop/assets"}, QueuePolicy: QueuePolicy{"fifo", 600, 86400, 120}, Limits: Limits{1000, 600, 900}, Theme: Theme{"calm", "잠시만 기다려 주세요", "순서대로 안내합니다.", "#315b4a", "ko", false, ""}, Active: true}}}
}
func TestConfigValidation(t *testing.T) {
	if fixture().Validate() != nil {
		t.Fatal("valid rejected")
	}
	for _, mutate := range []func(*Config){
		func(c *Config) { c.Rooms[0].QueuePolicy.Kind = "lottery" }, func(c *Config) { c.Profile = "unknown" },
		func(c *Config) { c.Rooms[0].Origin = "https://user:secret@origin.test" }, func(c *Config) { c.Rooms[0].HealthURL = "https://other.test/health" },
		func(c *Config) { c.Rooms[0].Theme.Message = "<script>alert(1)</script>" }, func(c *Config) { c.Rooms[0].Theme.PrimaryColor = "red;" },
		func(c *Config) { c.Rooms[0].Theme.TemplateID = "external" }, func(c *Config) { c.Rooms[0].Limits.AdmissionsPerMinute = 6001 },
		func(c *Config) { c.Rooms[0].ProtectPrefixes = []string{"/shop/../api"} }, func(c *Config) { c.Rooms[0].ProtectPrefixes = []string{"/_wr"} },
		func(c *Config) { c.Rooms[0].ExcludePrefixes = nil }, func(c *Config) { c.Rooms[0].Hostname = "Shop.test" },
	} {
		c := fixture()
		mutate(&c)
		if c.Validate() == nil {
			t.Fatal("unsafe config accepted")
		}
	}
}
func TestRouteMatching(t *testing.T) {
	c := fixture()
	for path, want := range map[string]string{"/shop": "protected", "/shop/cart": "protected", "/shopping": "unprotected", "/shop/assets/logo.png": "excluded", "/_wr/v1": "reserved", "/shop/../cart": "invalid", "/shop%2fcart": "invalid", "/shop/%252e": "invalid", "/shop?source=test": "protected"} {
		u, e := url.Parse(path)
		if e != nil {
			t.Fatal(e)
		}
		if got := c.MatchURL("shop.example.test", u); got.Decision != want {
			t.Errorf("%s: %s != %s", path, got.Decision, want)
		}
	}
	u, _ := url.Parse("/shop")
	if c.MatchURL("evil.test", u).Decision != "unprotected" {
		t.Fatal("hostname ignored")
	}
}
func TestRouteConflicts(t *testing.T) {
	c := fixture()
	other := c.Rooms[0]
	other.ID = "second"
	other.PublicID = strings.Repeat("b", 20)
	other.ProtectPrefixes = []string{"/shop/cart"}
	c.Rooms = append(c.Rooms, other)
	if c.Validate() != ErrConflict {
		t.Fatal("overlap accepted")
	}
	c.Rooms[1].ProtectPrefixes = []string{"/shopping"}
	if c.Validate() != nil {
		t.Fatal("segment boundary conflict")
	}
	c.Rooms[1].ProtectPrefixes = []string{"/shop"}
	c.Rooms[1].Active = false
	if c.Validate() != nil {
		t.Fatal("inactive draft conflicts")
	}
}
func TestExactConfigDecode(t *testing.T) {
	raw := string(fixture().Bytes())
	var out Config
	if DecodeExact([]byte(raw), &out) != nil || out.Validate() != nil {
		t.Fatal("roundtrip")
	}
	for _, bad := range []string{strings.Replace(raw, `"revision":0`, `"revision":0,"revision":1`, 1), strings.Replace(raw, `"revision"`, `"Revision"`, 1), strings.Replace(raw, `"rooms":[`, `"unknown":1,"rooms":[`, 1), strings.Replace(raw, `"revision":0,`, "", 1), raw + `{}`, strings.Replace(raw, `"excludePrefixes":["/shop/assets"]`, `"excludePrefixes":null`, 1), strings.Replace(raw, `"title":"잠시만 기다려 주세요"`, `"title":"`+string([]byte{0x92})+`"`, 1)} {
		if DecodeExact([]byte(bad), &out) == nil {
			t.Fatal("ambiguous input accepted")
		}
	}
	if DecodeExact([]byte(raw), (*Config)(nil)) == nil {
		t.Fatal("nil output accepted")
	}
}
func FuzzConfigDecode(f *testing.F) {
	f.Add(fixture().Bytes())
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		var c Config
		if DecodeExact(raw, &c) != nil {
			return
		}
		b, e := json.Marshal(c)
		if e != nil {
			t.Fatal(e)
		}
		var next Config
		if len(b) <= MaxConfigBytes && DecodeExact(b, &next) != nil {
			t.Fatal("accepted config cannot roundtrip")
		}
	})
}
