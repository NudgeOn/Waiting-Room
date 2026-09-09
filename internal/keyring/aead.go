// SPDX-License-Identifier: Apache-2.0
package keyring

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"strings"
)

func aead(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrInvalid
	}
	return cipher.NewGCM(block)
}
func Seal(k Key, purpose string, plain, aad []byte) (string, error) {
	key := k.Replay
	if purpose == "return" || purpose == "browser-join" {
		key = k.Return
	} else if purpose != "replay" {
		return "", ErrInvalid
	}
	c, err := aead(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, c.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", err
	}
	bound := append([]byte("wr-"+purpose+"/v2/"+k.ID+"/"), aad...)
	return k.ID + "." + base64.RawURLEncoding.EncodeToString(c.Seal(nonce, nonce, plain, bound)), nil
}
func (s Set) Open(purpose, sealed string, aad, legacyAAD []byte) ([]byte, error) {
	if len(sealed) > 8192 {
		return nil, ErrInvalid
	}
	id, encoded, versioned := strings.Cut(sealed, ".")
	if !versioned {
		id = "v1"
		encoded = sealed
	}
	k, ok := s.Find(id)
	if !ok {
		return nil, ErrInvalid
	}
	key := k.Replay
	if purpose == "return" || purpose == "browser-join" {
		key = k.Return
	} else if purpose != "replay" {
		return nil, ErrInvalid
	}
	c, err := aead(key)
	if err != nil {
		return nil, err
	}
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(data) < c.NonceSize()+c.Overhead() {
		return nil, ErrInvalid
	}
	bound := legacyAAD
	if versioned {
		bound = append([]byte("wr-"+purpose+"/v2/"+id+"/"), aad...)
	}
	plain, err := c.Open(nil, data[:c.NonceSize()], data[c.NonceSize():], bound)
	if err != nil {
		return nil, ErrInvalid
	}
	return plain, nil
}
