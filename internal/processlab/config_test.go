// SPDX-License-Identifier: Apache-2.0
package processlab

import (
	"bufio"
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func TestGatewaySignedBinding(t *testing.T) {
	in := gatewayInput{Coordinator: "http://127.0.0.1:10001", Origin: "http://127.0.0.1:10002", Service: "DO-NOT-LOG-SERVICE-SECRET", Public: make([]byte, 32), ReturnKey: []byte("DO-NOT-LOG-RETURN-SECRET-32-BYTES"), Installation: "test-install"}
	now := time.Now()
	signed, err := signGatewayConfig(in, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(signed.SignedConfig, []byte(in.Service)) || bytes.Contains(signed.SignedConfig, in.ReturnKey) {
		t.Fatal("signed config contains raw secret")
	}
	if _, err = gatewayTrust(signed).Current(); err != nil {
		t.Fatal("valid binding rejected")
	}
	for _, edit := range []func(*gatewayInput){func(g *gatewayInput) { g.Origin = "http://127.0.0.1:10003" }, func(g *gatewayInput) { g.Coordinator = "http://127.0.0.1:10004" }, func(g *gatewayInput) { g.Service = "different" }, func(g *gatewayInput) { g.ReturnKey = []byte("different") }, func(g *gatewayInput) { g.Public = []byte("different") }, func(g *gatewayInput) { g.Installation = "other" }, func(g *gatewayInput) { g.SignedConfig = nil }, func(g *gatewayInput) { g.ConfigTrust = nil }} {
		copy := signed
		edit(&copy)
		if _, err = gatewayTrust(copy).Current(); err == nil {
			t.Fatal("tampered runtime binding accepted")
		}
	}
}

func TestCoordinatorSignedBindingBeforeStore(t *testing.T) {
	in := coordinatorInput{Address: "127.0.0.1:16379", Namespace: "wr:lab:process-0123456789abcdef0123456789abcdef"}
	now := time.Now()
	signed, e := signCoordinatorConfig(in, now, time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = coordinatorTrust(signed).Current(); e != nil {
		t.Fatal("valid coordinator config")
	}
	copy := signed
	copy.Namespace = "wr:lab:process-1123456789abcdef0123456789abcdef"
	if _, e = coordinatorTrust(copy).Current(); e == nil {
		t.Fatal("changed namespace accepted")
	}
	// Missing signature must be rejected before any Valkey connection or writes.
	var output bytes.Buffer
	raw := `{"address":"127.0.0.1:16379","namespace":"wr:lab:process-0123456789abcdef0123456789abcdef"}` + "\n"
	if e = runRole(context.Background(), "coordinator", bufio.NewReader(strings.NewReader(raw)), &output); e == nil || output.Len() != 0 {
		t.Fatal("unsigned Coordinator started")
	}
}
