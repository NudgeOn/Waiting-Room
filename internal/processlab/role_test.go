// SPDX-License-Identifier: Apache-2.0
package processlab

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestPrivateConfigBounds(t *testing.T) {
	for _, raw := range []string{"", "{}", "null\n", "[]\n", "{} {}\n", "{\"secret\":\"DO-NOT-ECHO\"}\n", strings.Repeat("x", 16385) + "\n"} {
		var dst struct{}
		err := configLine(bufio.NewReaderSize(strings.NewReader(raw), 16384), &dst)
		if err == nil || strings.Contains(err.Error(), "DO-NOT-ECHO") {
			t.Fatal("invalid IPC accepted or echoed")
		}
	}
	var dst struct{}
	if configLine(bufio.NewReader(strings.NewReader("{}\n")), &dst) != nil {
		t.Fatal("valid IPC rejected")
	}
}

func TestRoleConfigurationSeparation(t *testing.T) {
	for _, tc := range []struct{ role, config string }{
		{"unknown", "{}\n"},
		{"origin", "{\"service\":\"DO-NOT-ECHO\"}\n"},
		{"coordinator", "{\"address\":\"example.com:6379\",\"namespace\":\"wr:lab:process-x\"}\n"},
		{"coordinator", "{\"address\":\"127.0.0.1:16379\",\"namespace\":\"production\"}\n"},
		{"gateway", "{\"address\":\"127.0.0.1:16379\"}\n"},
		{"gateway", "{\"private\":\"DO-NOT-ECHO\"}\n"},
		{"gateway", "{}\n"},
	} {
		var output bytes.Buffer
		if err := runRole(context.Background(), tc.role, strings.NewReader(tc.config), &output); err == nil || output.Len() != 0 || strings.Contains(err.Error(), "DO-NOT-ECHO") {
			t.Fatal("invalid role config accepted or leaked")
		}
	}
	// Ordinary file redirection must not expose private readiness IPC.
	f, err := os.CreateTemp(t.TempDir(), "not-a-pipe")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if RunChild(context.Background(), "origin", f, f) == nil {
		t.Fatal("file IPC accepted")
	}
}

func TestPublicReadinessAndSummary(t *testing.T) {
	for _, bad := range []string{"http://example.com:1234", "https://127.0.0.1:1234", "http://127.0.0.1:0", "http://127.0.0.1:99999", "http://127.0.0.1:http", "http://127.0.0.1:1234/x", "http://127.0.0.1:1234?secret"} {
		if publicURL(bad) {
			t.Fatal("unsafe readiness address")
		}
	}
	if !publicURL("http://127.0.0.1:1234") {
		t.Fatal("loopback rejected")
	}
	c := &Cluster{gateways: []string{"http://127.0.0.1:1234"}, children: []*child{{ready: ready{PID: 123, Service: "DO-NOT-ECHO", Public: []byte("DO-NOT-ECHO")}}}}
	b, _ := json.Marshal(c.Summary())
	if bytes.Contains(b, []byte("DO-NOT-ECHO")) {
		t.Fatal("summary leaks private IPC")
	}
	copy := c.Gateways()
	copy[0] = "changed"
	if c.gateways[0] == "changed" {
		t.Fatal("mutable gateway slice exposed")
	}
}

func TestCanceledAndRelativeProcessLaunch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if p, err := startChild(ctx, "/not-executed", "origin", struct{}{}); err == nil || p != nil {
		t.Fatal("canceled launch accepted")
	}
	if p, err := startChild(context.Background(), "search-PATH", "origin", struct{}{}); err == nil || p != nil {
		t.Fatal("PATH launch accepted")
	}
}
