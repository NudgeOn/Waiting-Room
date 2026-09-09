// SPDX-License-Identifier: Apache-2.0
// Package keyring holds role-scoped deployment keys. It performs no key I/O.
package keyring

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"time"
)

const Overlap = 24*time.Hour + 30*time.Second

var ErrInvalid = errors.New("invalid deployment keyring")
var idPattern = regexp.MustCompile(`^(v1|[a-f0-9]{32})$`)

type Key struct {
	ID                                              string `json:"id"`
	Since                                           int64  `json:"since"`
	ConfigPublic, AdmissionPublic                   []byte
	ConfigPrivate, AdmissionPrivate, Replay, Return []byte `json:",omitempty"`
}
type Set struct {
	Generation int64  `json:"generation"`
	Phase      string `json:"phase"`
	Current    Key    `json:"current"`
	Previous   *Key   `json:"previous,omitempty"`
	Next       *Key   `json:"next,omitempty"`
}

func (Set) String() string         { return "[REDACTED_KEYRING]" }
func (Set) GoString() string       { return "[REDACTED_KEYRING]" }
func (Key) String() string         { return "[REDACTED_KEY]" }
func (Key) GoString() string       { return "[REDACTED_KEY]" }
func (k Key) ConfigKid() string    { return "config-" + k.ID }
func (k Key) AdmissionKid() string { return "admission-" + k.ID }
func (k Key) ReturnKid() string    { return "return-" + k.ID }
func (s Set) Keys() []Key {
	keys := []Key{s.Current}
	if s.Previous != nil {
		keys = append(keys, *s.Previous)
	}
	if s.Next != nil {
		keys = append(keys, *s.Next)
	}
	return keys
}
func (s Set) Validate(role string) error {
	if s.Generation < 1 || s.Generation >= 9007199254740990 || (s.Phase != "stable" && s.Phase != "staged" && s.Phase != "active") || (s.Phase == "staged") != (s.Next != nil) || (s.Phase == "active") != (s.Previous != nil) {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, k := range s.Keys() {
		if !idPattern.MatchString(k.ID) || seen[k.ID] || k.Since < 0 || k.Since >= 9007199254740990 || len(k.ConfigPublic) != 32 || len(k.AdmissionPublic) != 32 {
			return ErrInvalid
		}
		seen[k.ID] = true
		cp, ap, rp, rt := role == "control" || role == "owner", role == "coordinator" || role == "owner", role == "coordinator" || role == "owner", role == "gateway" || role == "owner"
		for _, check := range []struct {
			data, pub []byte
			want      bool
		}{{k.ConfigPrivate, k.ConfigPublic, cp}, {k.AdmissionPrivate, k.AdmissionPublic, ap}} {
			if check.want {
				if len(check.data) != 64 || !bytes.Equal(ed25519.PrivateKey(check.data).Public().(ed25519.PublicKey), check.pub) || !bytes.Equal(ed25519.NewKeyFromSeed(check.data[:32]), check.data) {
					return ErrInvalid
				}
			} else if len(check.data) != 0 {
				return ErrInvalid
			}
		}
		for _, check := range []struct {
			data []byte
			want bool
		}{{k.Replay, rp}, {k.Return, rt}} {
			if check.want {
				if len(check.data) != 32 || bytes.Equal(check.data, make([]byte, 32)) {
					return ErrInvalid
				}
			} else if len(check.data) != 0 {
				return ErrInvalid
			}
		}
	}
	if s.Previous != nil && (s.Current.Since <= s.Previous.Since || s.Current.Since == 0) {
		return ErrInvalid
	}
	if role != "owner" && role != "control" && role != "coordinator" && role != "gateway" {
		return ErrInvalid
	}
	return nil
}
func (s Set) ForRole(role string) Set {
	sanitize := func(k Key) Key {
		if role != "control" {
			k.ConfigPrivate = nil
		}
		if role != "coordinator" {
			k.AdmissionPrivate = nil
			k.Replay = nil
		}
		if role != "gateway" {
			k.Return = nil
		}
		return k
	}
	out := s
	out.Current = sanitize(s.Current)
	if s.Previous != nil {
		k := sanitize(*s.Previous)
		out.Previous = &k
	}
	if s.Next != nil {
		k := sanitize(*s.Next)
		out.Next = &k
	}
	return out
}

// Digest binds every role's ACK to the same public keys and lifecycle metadata.
func (s Set) Digest() string {
	out := s.ForRole("")
	raw, _ := json.Marshal(out)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func (s Set) ConfigTrust() map[string]ed25519.PublicKey {
	out := map[string]ed25519.PublicKey{}
	for _, k := range s.Keys() {
		out[k.ConfigKid()] = k.ConfigPublic
	}
	return out
}

// Selecting by the immutable promotion timestamp preserves exact claim retries.
// The local owner must stop every signer before recording Current.Since.
func (s Set) AdmissionAt(promoted int64) Key {
	if s.Previous != nil && promoted < s.Current.Since {
		return *s.Previous
	}
	return s.Current
}
func (s Set) Find(id string) (Key, bool) {
	for _, k := range s.Keys() {
		if k.ID == id {
			return k, true
		}
	}
	return Key{}, false
}
