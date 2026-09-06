// SPDX-License-Identifier: Apache-2.0
package adminauth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net"
	"net/http"
	"net/url"
)

type OriginPolicy struct{ origin string }

// Origin is deployment-pinned, never taken from Request.Host or Forwarded.
// HTTP is allowed only when explicitly enabled for a literal-loopback local lab.
func NewOriginPolicy(origin string, allowLoopbackHTTP bool) (OriginPolicy, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return OriginPolicy{}, ErrForbidden
	}
	for _, c := range origin {
		if c <= 32 || c == 127 || c == 92 {
			return OriginPolicy{}, ErrForbidden
		}
	}
	if u.Scheme != "https" {
		ip := net.ParseIP(u.Hostname())
		if u.Scheme != "http" || !allowLoopbackHTTP || ip == nil || !ip.IsLoopback() {
			return OriginPolicy{}, ErrForbidden
		}
	}
	return OriginPolicy{origin}, nil
}

// Raw value is delivered only to an authenticated same-origin session holder.
// Persist only the hash, bound to the corresponding server-side session.
func NewCSRFToken() (string, [32]byte, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", [32]byte{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw[:])
	return token, sha256.Sum256([]byte(token)), nil
}

func (p OriginPolicy) CheckMutation(r *http.Request, sessionTokenHash [32]byte) error {
	if p.origin == "" || sessionTokenHash == [32]byte{} {
		return ErrForbidden
	}
	switch r.Method {
	case "POST", "PUT", "PATCH", "DELETE":
	default:
		return ErrForbidden
	}
	if len(r.Header.Values("Origin")) != 1 || r.Header.Get("Origin") != p.origin || len(r.Header.Values("X-CSRF-Token")) != 1 {
		return ErrForbidden
	}
	token := r.Header.Get("X-CSRF-Token")
	if len(token) != 43 {
		return ErrForbidden
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || len(decoded) != 32 {
		return ErrForbidden
	}
	actual := sha256.Sum256([]byte(token))
	if subtle.ConstantTimeCompare(actual[:], sessionTokenHash[:]) != 1 {
		return ErrForbidden
	}
	return nil
}
