// SPDX-License-Identifier: Apache-2.0
// Package adminauth defines Backoffice authorization primitives. It does not expose
// HTTP login endpoints or substitute for authoritative account/session storage.
package adminauth

type Role string

const (
	Admin             Role = "admin"
	Operator          Role = "operator"
	Viewer            Role = "viewer"
	CapabilityVersion      = 1
)

type Action string

const (
	ReadDashboard   Action = "dashboard.read"
	ReadConfig      Action = "config.read"
	WriteConfig     Action = "config.write"
	ReadRuntime     Action = "runtime.read"
	OperateRuntime  Action = "runtime.operate"
	InstantOff      Action = "runtime.instant_off"
	NewEpoch        Action = "runtime.new_epoch"
	ReadEvents      Action = "events.read"
	WriteEvents     Action = "events.write"
	ReadLab         Action = "lab.read"
	RunLab          Action = "lab.run"
	ReadUsers       Action = "users.read"
	WriteUsers      Action = "users.write"
	ReadSecurity    Action = "security.read"
	WriteTOTPPolicy Action = "security.totp.write"
	ResetUserTOTP   Action = "security.totp.reset"
	RotateKeys      Action = "security.keys.rotate"
	ReadAudit       Action = "audit.read"
	ReadInstall     Action = "install.read"
)

type Capability struct {
	Action                   Action `json:"action"`
	RequiresReauthentication bool   `json:"requiresReauthentication"`
}
type rule struct {
	action         Action
	roles          uint8
	reauthenticate bool
}

const adminBit, operatorBit, viewerBit uint8 = 1, 2, 4
const allRoles = adminBit | operatorBit | viewerBit

var rules = [...]rule{
	{ReadDashboard, allRoles, false}, {ReadConfig, allRoles, false},
	{WriteConfig, adminBit, false}, {ReadRuntime, allRoles, false},
	{OperateRuntime, adminBit | operatorBit, false}, {InstantOff, adminBit, true},
	{NewEpoch, adminBit, true}, {ReadEvents, allRoles, false},
	{WriteEvents, adminBit | operatorBit, false}, {ReadLab, allRoles, false},
	{RunLab, adminBit | operatorBit, false}, {ReadUsers, adminBit, false},
	{WriteUsers, adminBit, true}, {ReadSecurity, adminBit, false},
	{WriteTOTPPolicy, adminBit, true}, {ResetUserTOTP, adminBit, true},
	{RotateKeys, adminBit, true}, {ReadAudit, allRoles, false}, {ReadInstall, allRoles, false},
}

func roleBit(role Role) uint8 {
	switch role {
	case Admin:
		return adminBit
	case Operator:
		return operatorBit
	case Viewer:
		return viewerBit
	}
	return 0
}

// Requirement is a policy lookup, not proof of an authenticated session or reauth.
// WriteConfig includes templates; OperateRuntime excludes instant OFF/new epoch.
func Requirement(role Role, action Action) (Capability, bool) {
	bit := roleBit(role)
	for _, rule := range rules {
		if rule.action == action && rule.roles&bit != 0 {
			return Capability{action, rule.reauthenticate}, true
		}
	}
	return Capability{}, false
}

// Capabilities is a fresh, stable-order projection for the future UI and API.
// Client-provided capabilities are never an authorization credential.
func Capabilities(role Role) []Capability {
	out := make([]Capability, 0, len(rules))
	for _, rule := range rules {
		if item, ok := Requirement(role, rule.action); ok {
			out = append(out, item)
		}
	}
	return out
}
