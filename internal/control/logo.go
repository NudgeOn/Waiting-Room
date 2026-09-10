// SPDX-License-Identifier: Apache-2.0
package control

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
)

// Logos share the bounded signed configuration and its backup/restore lifetime.
// Limit compressed input before decoding and dimensions before allocating pixels.
const MaxLogoBytes = 16 * 1024
const MaxLogoDimension = 256
const MaxLogoPixels = 65536
const MaxLogoBase64 = (MaxLogoBytes + 2) / 3 * 4

// SanitizeLogo accepts only base64 PNG/JPEG pixels and returns canonical PNG.
// Metadata, animation, trailing content, palettes and original encoding are not
// copied. No file name, MIME claim, external URL or data URL is trusted.
func SanitizeLogo(encoded string) (string, error) {
	if encoded == "" {
		return "", nil
	}
	if len(encoded) > MaxLogoBase64 {
		return "", ErrInvalid
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(raw) > MaxLogoBytes || base64.StdEncoding.EncodeToString(raw) != encoded {
		return "", ErrInvalid
	}
	var cfg image.Config
	var decode func(*bytes.Reader) (image.Image, error)
	switch {
	case bytes.HasPrefix(raw, []byte("\x89PNG\r\n\x1a\n")):
		cfg, err = png.DecodeConfig(bytes.NewReader(raw))
		decode = func(r *bytes.Reader) (image.Image, error) { return png.Decode(r) }
	case bytes.HasPrefix(raw, []byte{0xff, 0xd8, 0xff}):
		cfg, err = jpeg.DecodeConfig(bytes.NewReader(raw))
		decode = func(r *bytes.Reader) (image.Image, error) { return jpeg.Decode(r) }
	default:
		return "", ErrInvalid
	}
	if err != nil || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > MaxLogoDimension || cfg.Height > MaxLogoDimension || cfg.Width*cfg.Height > MaxLogoPixels {
		return "", ErrInvalid
	}
	source, err := decode(bytes.NewReader(raw))
	if err != nil || source.Bounds().Dx() != cfg.Width || source.Bounds().Dy() != cfg.Height {
		return "", ErrInvalid
	}
	pixels := image.NewNRGBA(image.Rect(0, 0, cfg.Width, cfg.Height))
	draw.Draw(pixels, pixels.Bounds(), source, source.Bounds().Min, draw.Src)
	var output bytes.Buffer
	if png.Encode(&output, pixels) != nil || output.Len() > MaxLogoBytes {
		return "", ErrInvalid
	}
	return base64.StdEncoding.EncodeToString(output.Bytes()), nil
}

// LogoBytes accepts only the bounded, metadata-free PNG representation written
// above. It does not compare compressed output with the current Go encoder: a
// future encoder change must not invalidate previously signed/backup images.
// This is also checked at Gateway construction, before serving any user request.
func LogoBytes(encoded string) ([]byte, error) {
	if encoded == "" {
		return nil, nil
	}
	if len(encoded) > MaxLogoBase64 {
		return nil, ErrInvalid
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(raw) > MaxLogoBytes || base64.StdEncoding.EncodeToString(raw) != encoded || !bytes.HasPrefix(raw, []byte("\x89PNG\r\n\x1a\n")) {
		return nil, ErrInvalid
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(raw))
	if err != nil || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > MaxLogoDimension || cfg.Height > MaxLogoDimension || cfg.Width*cfg.Height > MaxLogoPixels {
		return nil, ErrInvalid
	}
	end := false
	for at := 8; at < len(raw); {
		if len(raw)-at < 12 {
			return nil, ErrInvalid
		}
		size := int64(binary.BigEndian.Uint32(raw[at : at+4]))
		if size > int64(len(raw)-at-12) {
			return nil, ErrInvalid
		}
		next := at + 12 + int(size)
		switch string(raw[at+4 : at+8]) {
		case "IHDR":
			if at != 8 || size != 13 || raw[at+16] != 8 || (raw[at+17] != 2 && raw[at+17] != 6) {
				return nil, ErrInvalid
			}
		case "IDAT":
		case "IEND":
			if next != len(raw) || size != 0 {
				return nil, ErrInvalid
			}
			end = true
		default:
			return nil, ErrInvalid
		}
		at = next
	}
	if !end {
		return nil, ErrInvalid
	}
	if _, err := png.Decode(bytes.NewReader(raw)); err != nil {
		return nil, ErrInvalid
	}
	return raw, nil
}

// NormalizeThemeImages is called only on a submitted draft after authorization
// and durable replay lookup. Saved and signed configurations must already be
// canonical; their validation never repairs untrusted persisted data.
func (c *Config) NormalizeThemeImages() error {
	for i := range c.Rooms {
		logo, err := SanitizeLogo(c.Rooms[i].Theme.LogoImage)
		if err != nil {
			return ErrInvalid
		}
		c.Rooms[i].Theme.LogoImage = logo
	}
	return nil
}
