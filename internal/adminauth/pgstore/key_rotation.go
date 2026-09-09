// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	_ "embed"
	"waiting-room/internal/keyring"
)

//go:embed migrations/014_key_rotation.sql
var Migration014 string

// WithDeploymentKeys is called before the service is published to any goroutine.
func (s *PublicationService) WithDeploymentKeys(keys *keyring.Set) error {
	if keys == nil {
		return nil
	}
	if keys.Validate("control") != nil {
		return keyring.ErrInvalid
	}
	s.kid = keys.Current.ConfigKid()
	s.key = append(s.key[:0:0], keys.Current.ConfigPrivate...)
	s.keyGeneration = keys.Generation
	s.keyDigest = keys.Digest()
	return nil
}
