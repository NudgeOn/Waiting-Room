// SPDX-License-Identifier: Apache-2.0
package localcontrol

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRoleIdentityProvisioningStableAndSeparated(t *testing.T) {
	dir := t.TempDir()
	_ = os.Chmod(dir, 0700)
	s, err := createState(dir)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "nodes")
	if err = ProvisionIdentities(s, root); err != nil {
		t.Fatal(err)
	}
	gateway, err := LoadIdentity(filepath.Join(root, "gateway"), "gateway")
	if err != nil {
		t.Fatal(err)
	}
	if len(gateway.ConfigPrivate) != 0 || len(gateway.AdmissionPrivate) != 0 || len(gateway.ReplayKey) != 0 || gateway.ValkeyPassword != "" || len(gateway.ReturnKey) != 32 {
		t.Fatal("gateway privilege leak")
	}
	raw, _ := json.Marshal(gateway)
	if bytes.Contains(raw, []byte("PrivateKey")) {
		t.Fatal("secret JSON leak")
	}
	before, _ := os.ReadFile(filepath.Join(root, "gateway", "identity.json"))
	if err = ProvisionIdentities(s, root); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(root, "gateway", "identity.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("initialization rotated keys")
	}
	coordinator, err := LoadIdentity(filepath.Join(root, "coordinator"), "coordinator")
	if err != nil {
		t.Fatal(err)
	}
	if len(coordinator.ConfigPrivate) != 0 || len(coordinator.ReturnKey) != 0 || len(coordinator.AdmissionPrivate) != 64 || len(coordinator.ReplayKey) != 32 {
		t.Fatal("coordinator key separation")
	}
	s.Vault[0] ^= 1
	if ProvisionIdentities(s, root) == nil {
		t.Fatal("wrong installation accepted")
	}
}
