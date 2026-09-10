// SPDX-License-Identifier: Apache-2.0
package control

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math/rand/v2"
	"strings"
	"testing"

	"waiting-room/internal/configtrust"
)

func logoFixture(t *testing.T, format string) []byte {
	t.Helper()
	im := image.NewNRGBA(image.Rect(0, 0, 32, 16))
	for y := range 16 {
		for x := range 32 {
			im.SetNRGBA(x, y, color.NRGBA{uint8(x * 8), uint8(y * 16), 40, 255})
		}
	}
	var b bytes.Buffer
	var err error
	if format == "jpeg" {
		err = jpeg.Encode(&b, im, nil)
	} else {
		err = png.Encode(&b, im)
	}
	if err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestLogoSanitizerPixelsAndCanonicalRoundTrip(t *testing.T) {
	for _, format := range []string{"png", "jpeg"} {
		t.Run(format, func(t *testing.T) {
			raw := logoFixture(t, format)
			withTrailer := append(append([]byte(nil), raw...), []byte("<svg onload='alert(1)'>private-metadata</svg>")...)
			encoded, err := SanitizeLogo(base64.StdEncoding.EncodeToString(withTrailer))
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := LogoBytes(encoded)
			if err != nil || bytes.Contains(decoded, []byte("private-metadata")) || !bytes.HasPrefix(decoded, []byte("\x89PNG")) {
				t.Fatal("unsafe or noncanonical output")
			}
			got, err := png.Decode(bytes.NewReader(decoded))
			if err != nil || got.Bounds() != image.Rect(0, 0, 32, 16) {
				t.Fatal("pixels not retained")
			}
			var want image.Image
			if format == "jpeg" {
				want, err = jpeg.Decode(bytes.NewReader(raw))
			} else {
				want, err = png.Decode(bytes.NewReader(raw))
			}
			if err != nil {
				t.Fatal(err)
			}
			for y := range 16 {
				for x := range 32 {
					if color.NRGBAModel.Convert(got.At(x, y)) != color.NRGBAModel.Convert(want.At(x, y)) {
						t.Fatal("pixel changed")
					}
				}
			}
			if again, err := SanitizeLogo(encoded); err != nil || again != encoded {
				t.Fatal("canonical PNG not stable")
			}
			c := fixture()
			c.Rooms[0].Theme.LogoImage = base64.StdEncoding.EncodeToString(withTrailer)
			if c.Validate() == nil {
				t.Fatal("untrusted persisted bytes repaired during validation")
			}
			if c.NormalizeThemeImages() != nil || c.Validate() != nil || c.Rooms[0].Theme.LogoImage != encoded {
				t.Fatal("draft normalization")
			}
		})
	}
}

func TestLogoSanitizerRejectsUnsafeAndUnboundedInput(t *testing.T) {
	valid := logoFixture(t, "png")
	bomb := append([]byte(nil), valid...)
	binary.BigEndian.PutUint32(bomb[16:20], 100000)
	binary.BigEndian.PutUint32(bomb[29:33], crc32.ChecksumIEEE(bomb[12:29]))
	for _, raw := range [][]byte{[]byte("<svg><script>bad()</script></svg>"), []byte("GIF89a"), valid[:len(valid)/2], bomb, bytes.Repeat([]byte{1}, MaxLogoBytes+1)} {
		if _, err := SanitizeLogo(base64.StdEncoding.EncodeToString(raw)); err == nil {
			t.Fatal("invalid image accepted")
		}
	}
	for _, value := range []string{"https://example.test/logo.png", "data:image/png;base64," + base64.StdEncoding.EncodeToString(valid), "%%%", base64.StdEncoding.EncodeToString(valid) + "\n"} {
		if _, err := SanitizeLogo(value); err == nil {
			t.Fatal("ambiguous base64 accepted")
		}
	}
	if got, err := SanitizeLogo(""); err != nil || got != "" {
		t.Fatal("logo removal")
	}
}

func TestLogoSanitizerBoundsReencodedSize(t *testing.T) {
	// A compact JPEG can expand past the PNG output budget even within pixel limits.
	im := image.NewNRGBA(image.Rect(0, 0, 256, 256))
	random := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < len(im.Pix); i += 4 {
		im.Pix[i], im.Pix[i+1], im.Pix[i+2], im.Pix[i+3] = uint8(random.Uint32()), uint8(random.Uint32()), uint8(random.Uint32()), 255
	}
	var b bytes.Buffer
	if jpeg.Encode(&b, im, &jpeg.Options{Quality: 1}) != nil || b.Len() > MaxLogoBytes {
		t.Fatal("fixture exceeds input bound")
	}
	if _, err := SanitizeLogo(base64.StdEncoding.EncodeToString(b.Bytes())); err == nil {
		t.Fatal("PNG output exceeded size budget")
	}
}

func TestDraftBudgetLeavesRoomForFutureSignedRuntime(t *testing.T) {
	c := fixture()
	logo, err := SanitizeLogo(base64.StdEncoding.EncodeToString(logoFixture(t, "png")))
	if err != nil {
		t.Fatal(err)
	}
	c.Rooms[0].Theme.LogoImage = logo
	if !c.FitsDeliveryBudget() {
		t.Fatal("small logo budget")
	}
	key := ed25519.NewKeyFromSeed(make([]byte, 32))
	for n := 1; n <= 100; n++ {
		if n > 1 {
			r := c.Rooms[0]
			r.ID = "r" + strings.Repeat("x", n/26) + string(rune('a'+n%26))
			r.Active = false
			c.Rooms = append(c.Rooms, r)
		}
		if !c.FitsDeliveryBudget() {
			if len(c.Bytes()) >= MaxConfigBytes {
				t.Fatal("must reserve runtime/envelope space before raw draft reaches wire limit")
			}
			return
		}
		d := Delivery{Config: c, Runtimes: []RoomRuntime{}}
		for _, r := range c.Rooms {
			rt := InitialRuntime(r)
			rt.Revision = 9007199254740989
			rt.Epoch = 9007199254740989
			rt.RecoveryUntil = 9007199254740989
			rt.Mode = "RECOVERY_HOLD"
			rt.EventState = "paused_by_override"
			d.Runtimes = append(d.Runtimes, RoomRuntime{r.ID, rt})
		}
		if _, err := configtrust.Sign(key, configtrust.Snapshot{SchemaVersion: 1, Installation: strings.Repeat("i", 80), Generation: 9007199254740991, Revision: 9007199254740989, IssuedAt: 253402127999, ExpiresAt: 253402214399, Kid: strings.Repeat("k", 80), Payload: d.Bytes()}); err != nil {
			t.Fatal("saved draft cannot fit maximum envelope")
		}
	}
	t.Fatal("budget fixture did not reach limit")
}
