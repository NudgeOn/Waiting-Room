// SPDX-License-Identifier: Apache-2.0
package localcontrol

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func QueueOwnerPassword(s State) string { return hex.EncodeToString(derive(s, "valkey-owner")) }

// Domain separation preserves existing identities while granting the lab a
// separate ACL that cannot read or mutate any production queue keys.
func TrafficQueuePassword(coordinator string) string {
	h := hmac.New(sha256.New, []byte(coordinator))
	h.Write([]byte("waiting-room/traffic-lab/valkey/v1"))
	return hex.EncodeToString(h.Sum(nil))
}
func ProvisionQueueACL(s State, dir string, upgrade ...bool) error {
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
	// Valkey 8.1.6 checks default-user command permissions while replaying AOF
	// MULTI/EXEC (valkey-io/valkey#3983). The disabled, passwordless-account
	// permission set is for the internal loader only: off + resetpass means no
	// network client can authenticate as default, even with an arbitrary password.
	raw := []byte(fmt.Sprintf("user default off resetpass +@all ~* &*\nuser wr_coordinator on #%x ~wr:runtime:local* +@connection +info +fcall +fcall_ro +function|list +@hash +@sortedset +time +exists\nuser wr_initializer on #%x ~wr:runtime:local* +@connection +function|load +function|list\n", a, b))
	previous := append([]byte(nil), raw...)
	labPassword := sha256.Sum256([]byte(TrafficQueuePassword(coordinator)))
	raw = append(raw, []byte(fmt.Sprintf("user wr_traffic on #%x ~wr:lab:traffic{* +@connection +info +fcall +fcall_ro +function|list +@hash +@sortedset +time +exists +del +pexpire\n", labPassword))...)
	previousTraffic := append([]byte(nil), raw...)
	raw = bytes.Replace(raw, []byte("~wr:runtime:local* +@connection +function|load +function|list"), []byte("~wr:runtime:local* ~wr:maintenance:runtime:local* +@connection +function|load +function|list +fcall +fcall_ro +info +@hash +@sortedset +time +exists +type +dump"), 1)
	file := filepath.Join(dir, "users.acl")
	old, err := ReadPrivate(file, 4096)
	if errors.Is(err, os.ErrNotExist) {
		err = writePrivate(dir, "users.acl", raw, false)
	} else if err == nil && !bytes.Equal(raw, old) {
		// Only the exact previous generated ACL can be upgraded. Unknown edits
		// are never overwritten. No role password or credential is changed.
		legacy := bytes.Replace(previous, []byte("user default off resetpass +@all ~* &*\n"), []byte("user default off\n"), 1)
		if len(upgrade) != 1 || !upgrade[0] || (!bytes.Equal(legacy, old) && !bytes.Equal(previous, old) && !bytes.Equal(previousTraffic, old)) {
			return errors.New("queue ACL mismatch; explicit upgrade of known ACL required")
		}
		err = writePrivate(dir, "users.acl", raw, true)
	}
	if err != nil {
		return err
	}
	if err = os.Chown(file, 999, 999); err != nil {
		return err
	}
	return os.Chown(dir, 999, 999)
}
