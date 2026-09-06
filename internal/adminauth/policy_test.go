// SPDX-License-Identifier: Apache-2.0
package adminauth

import (
	"reflect"
	"testing"
)

func TestRoleActionMatrix(t *testing.T) {
	all := []Action{ReadDashboard, ReadConfig, WriteConfig, ReadRuntime, OperateRuntime, InstantOff, NewEpoch, ReadEvents, WriteEvents, ReadLab, RunLab, ReadUsers, WriteUsers, ReadSecurity, WriteTOTPPolicy, ResetUserTOTP, RotateKeys, ReadAudit, ReadInstall}
	expected := map[Role][]Action{
		Admin:    all,
		Operator: {ReadDashboard, ReadConfig, ReadRuntime, OperateRuntime, ReadEvents, WriteEvents, ReadLab, RunLab, ReadAudit, ReadInstall},
		Viewer:   {ReadDashboard, ReadConfig, ReadRuntime, ReadEvents, ReadLab, ReadAudit, ReadInstall},
	}
	for role, allow := range expected {
		for _, action := range all {
			want := false
			for _, candidate := range allow {
				if candidate == action {
					want = true
				}
			}
			_, got := Requirement(role, action)
			if got != want {
				t.Fatalf("%s %s: got %t, want %t", role, action, got, want)
			}
		}
	}
	if len(rules) != len(all) {
		t.Fatal("new rule requires explicit role matrix coverage")
	}
	seen := map[Action]bool{}
	for _, r := range rules {
		if seen[r.action] {
			t.Fatal("duplicate action")
		}
		seen[r.action] = true
	}
}

func TestCapabilityProjectionAndDefaultDeny(t *testing.T) {
	for _, role := range []Role{"", "ADMIN", "owner", "superadmin"} {
		if len(Capabilities(role)) != 0 {
			t.Fatal("unknown role got capabilities")
		}
		if _, ok := Requirement(role, WriteConfig); ok {
			t.Fatal("unknown role allowed")
		}
	}
	if _, ok := Requirement(Admin, "config.anything"); ok {
		t.Fatal("unknown action allowed")
	}
	before := Capabilities(Admin)
	copy := Capabilities(Admin)
	copy[0].Action = "tampered"
	if !reflect.DeepEqual(before, Capabilities(Admin)) {
		t.Fatal("registry mutated by caller")
	}
	if CapabilityVersion != 1 {
		t.Fatal("unexpected policy version")
	}
}

func TestDangerousActionsRequireReauthentication(t *testing.T) {
	dangerous := map[Action]bool{InstantOff: true, NewEpoch: true, WriteUsers: true, WriteTOTPPolicy: true, ResetUserTOTP: true, RotateKeys: true}
	for _, c := range Capabilities(Admin) {
		if c.RequiresReauthentication != dangerous[c.Action] {
			t.Fatal("reauth mismatch", c.Action)
		}
	}
	if _, ok := Requirement(Operator, WriteConfig); ok {
		t.Fatal("operator can change template")
	}
	if _, ok := Requirement(Viewer, OperateRuntime); ok {
		t.Fatal("viewer can operate room")
	}
}
