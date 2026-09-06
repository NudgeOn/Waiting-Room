// SPDX-License-Identifier: Apache-2.0
package admission

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
)

var ErrInvalid = errors.New("invalid admission")

type Claims struct {
	Kid       string `json:"kid"`
	Room      string `json:"room"`
	Epoch     uint64 `json:"epoch"`
	JTI       string `json:"jti"`
	Issued    int64  `json:"iat"`
	NotBefore int64  `json:"nbf"`
	Expires   int64  `json:"exp"`
	Audience  string `json:"aud"`
}

// Sign produces identical bytes for identical claims and key, including after retry.
func Sign(key ed25519.PrivateKey, c Claims) (string, error) {
	if len(key) != ed25519.PrivateKeySize {
		return "", ErrInvalid
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(raw)
	signature := ed25519.Sign(key, []byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}
func Verify(key ed25519.PublicKey, token, kid, room, aud string, epoch uint64, now time.Time, leeway time.Duration) (Claims, error) {
	var c Claims
	if len(key) != ed25519.PublicKeySize || len(token) > 2048 || leeway < 0 || leeway > 30*time.Second {
		return c, ErrInvalid
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return c, ErrInvalid
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !ed25519.Verify(key, []byte(parts[0]), sig) {
		return c, ErrInvalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return c, ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil {
		return c, ErrInvalid
	}
	if d.Decode(new(any)) != io.EOF {
		return c, ErrInvalid
	}
	n := now.Unix()
	skew := int64(leeway / time.Second)
	if c.Kid != kid || c.Room != room || c.Audience != aud || c.Epoch != epoch || c.JTI == "" || c.NotBefore != c.Issued || c.Expires <= c.Issued || c.Expires-c.Issued > 3600 || n+skew < c.NotBefore || n >= c.Expires+skew {
		return Claims{}, ErrInvalid
	}
	return c, nil
}
