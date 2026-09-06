// SPDX-License-Identifier: Apache-2.0
package localcontrol

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func QueueOwnerPassword(s State) string { return hex.EncodeToString(derive(s, "valkey-owner")) }
func ProvisionQueueACL(s State, dir string) error {
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() {
		return errors.New("queue config volume required")
	}
	if err = os.Chmod(dir, 0700); err != nil {
		return err
	}
	coordinator := hex.EncodeToString(derive(s, "valkey"))
	a := sha256.Sum256([]byte(coordinator))
	b := sha256.Sum256([]byte(QueueOwnerPassword(s)))
	raw := []byte(fmt.Sprintf("user default off\nuser wr_coordinator on #%x ~wr:runtime:local* +@connection +info +fcall +fcall_ro +function|list +@hash +@sortedset +time +exists\nuser wr_initializer on #%x ~wr:runtime:local* +@connection +function|load +function|list\n", a, b))
	file := filepath.Join(dir, "users.acl")
	old, err := ReadPrivate(file, 4096)
	if errors.Is(err, os.ErrNotExist) {
		err = writePrivate(dir, "users.acl", raw, false)
	} else if err == nil && !bytes.Equal(raw, old) {
		return errors.New("queue ACL mismatch")
	}
	if err != nil {
		return err
	}
	if err = os.Chown(file, 999, 999); err != nil {
		return err
	}
	return os.Chown(dir, 999, 999)
}
