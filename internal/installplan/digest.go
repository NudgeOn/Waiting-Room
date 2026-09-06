// SPDX-License-Identifier: Apache-2.0
package installplan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// Digest is SHA-256 of the compact JSON Build result without a trailing newline.
// It binds observations to every current planning value, including TOTP policy.
// It is not a signature or proof that an observation came from a real probe.
func Digest(in Input) (string, error) {
	p, err := Build(in)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(p)
	if err != nil {
		return "", ErrInput
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
