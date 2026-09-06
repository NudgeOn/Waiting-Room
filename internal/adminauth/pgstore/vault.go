// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"

	"waiting-room/internal/adminauth"
)

var safeID = regexp.MustCompile("^[A-Za-z0-9_-]{1,128}$")
var safeKeyID = regexp.MustCompile("^[A-Za-z0-9_-]{1,64}$")

// Vault is constructed with Control-only key material, never read from the DB.
// Key-file loading/rotation and enrollment UI are outside this storage slice.
type Vault struct{ keys map[string]cipher.AEAD }

func (Vault) String() string               { return "[REDACTED_VAULT]" }
func (Vault) GoString() string             { return "[REDACTED_VAULT]" }
func (Vault) MarshalJSON() ([]byte, error) { return json.Marshal("[REDACTED_VAULT]") }

func NewVault(keys map[string][]byte) (*Vault, error) {
	if len(keys) == 0 {
		return nil, adminauth.ErrAuthUnavailable
	}
	v := &Vault{keys: make(map[string]cipher.AEAD, len(keys))}
	for id, key := range keys {
		if !safeKeyID.MatchString(id) || len(key) != 32 {
			return nil, adminauth.ErrAuthUnavailable
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, adminauth.ErrAuthUnavailable
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, adminauth.ErrAuthUnavailable
		}
		v.keys[id] = aead
	}
	return v, nil
}
func associatedData(ref adminauth.CredentialRef, kid string) ([]byte, error) {
	if !safeID.MatchString(ref.UserID) || ref.Version == 0 || !safeKeyID.MatchString(kid) {
		return nil, adminauth.ErrAuthUnavailable
	}
	return []byte(fmt.Sprintf("wr/admin/totp/v1:%s:%d:%s", ref.UserID, ref.Version, kid)), nil
}
func (v *Vault) Seal(ref adminauth.CredentialRef, kid string, secret adminauth.Secret) ([]byte, error) {
	aad, err := associatedData(ref, kid)
	if err != nil {
		return nil, err
	}
	return v.sealAAD(aad, kid, secret)
}
func (v *Vault) sealEnrollment(ref adminauth.CredentialRef, kid string, token [32]byte, secret adminauth.Secret) ([]byte, error) {
	aad, err := associatedData(ref, kid)
	if err != nil {
		return nil, err
	}
	return v.sealAAD(append(aad, []byte(":enrollment:"+hex.EncodeToString(token[:]))...), kid, secret)
}
func (v *Vault) openEnrollment(ref adminauth.CredentialRef, kid string, token [32]byte, sealed []byte) (adminauth.Secret, error) {
	aad, err := associatedData(ref, kid)
	if err != nil {
		return adminauth.Secret{}, err
	}
	return v.openAAD(append(aad, []byte(":enrollment:"+hex.EncodeToString(token[:]))...), kid, sealed)
}
func (v *Vault) sealAAD(aad []byte, kid string, secret adminauth.Secret) ([]byte, error) {
	if v == nil {
		return nil, adminauth.ErrAuthUnavailable
	}
	aead := v.keys[kid]
	if aead == nil {
		return nil, adminauth.ErrAuthUnavailable
	}
	raw, err := secret.ProvisioningBase32()
	if err != nil {
		return nil, adminauth.ErrAuthUnavailable
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, adminauth.ErrAuthUnavailable
	}
	return aead.Seal(nonce, nonce, []byte(raw), aad), nil
}
func (v *Vault) open(ref adminauth.CredentialRef, kid string, sealed []byte) (adminauth.Secret, error) {
	aad, err := associatedData(ref, kid)
	if err != nil {
		return adminauth.Secret{}, err
	}
	return v.openAAD(aad, kid, sealed)
}
func (v *Vault) openAAD(aad []byte, kid string, sealed []byte) (adminauth.Secret, error) {
	if v == nil {
		return adminauth.Secret{}, adminauth.ErrAuthUnavailable
	}
	aead := v.keys[kid]
	if aead == nil || len(sealed) != 60 {
		return adminauth.Secret{}, adminauth.ErrAuthUnavailable
	}
	raw, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], aad)
	if err != nil {
		return adminauth.Secret{}, adminauth.ErrAuthUnavailable
	}
	defer clear(raw)
	secret, err := adminauth.ParseSecret(string(raw))
	if err != nil {
		return adminauth.Secret{}, adminauth.ErrAuthUnavailable
	}
	return secret, nil
}
