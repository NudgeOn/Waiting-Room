//go:build integration

// SPDX-License-Identifier: Apache-2.0
package processlab

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"waiting-room/internal/lab"
)

func buildLab(t *testing.T) string {
	t.Helper()
	if os.Getenv("WR_TEST_VALKEY") != "127.0.0.1:16379" {
		t.Fatal("dedicated WR_TEST_VALKEY required")
	}
	binary := filepath.Join(t.TempDir(), "wr-process-lab")
	cmd := exec.Command("go", "build", "-race", "-o", binary, "./cmd/wr-process-lab")
	cmd.Dir = "../.."
	if cmd.Run() != nil {
		t.Fatal("process lab build failed")
	}
	return binary
}

func get(t *testing.T, client *http.Client, method, address string, headers map[string]string) (int, []byte, http.Header) {
	t.Helper()
	req, err := http.NewRequest(method, address, nil)
	if err != nil {
		t.Fatal("request invalid")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal("request failed; credential-bearing URL omitted")
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 16384))
	if err != nil {
		t.Fatal("response read failed")
	}
	return resp.StatusCode, b, resp.Header
}
func client() *http.Client {
	return &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
func originCount(t *testing.T, c *Cluster) int {
	t.Helper()
	code, b, _ := get(t, client(), "GET", c.origin+"/__lab/count", nil)
	var data struct{ Count int }
	if code != 200 || json.Unmarshal(b, &data) != nil {
		t.Fatal("origin counter unavailable")
	}
	return data.Count
}
func assertClosed(t *testing.T, c *Cluster, killedCoordinator bool) {
	t.Helper()
	c.Close()
	for i, p := range c.children {
		select {
		case <-p.done:
		default:
			t.Fatal("child still running")
		}
		if killedCoordinator && i == 1 {
			if p.cmd.ProcessState == nil || p.cmd.ProcessState.ExitCode() != -1 {
				t.Error("faulted Coordinator was not terminated by a signal")
			}
		} else if p.cmd.ProcessState == nil || !p.cmd.ProcessState.Success() {
			t.Error("child exited with an error or race detector failure")
		}
	}
}

func TestProcessQuick20AndCoordinatorDeath(t *testing.T) {
	binary := buildLab(t)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	c, err := Start(ctx, binary)
	if err != nil {
		t.Fatal(err)
	}
	defer assertClosed(t, c, true)
	seen := map[int]bool{os.Getpid(): true}
	for _, pid := range c.Summary().ChildPIDs {
		if pid <= 0 || seen[pid] {
			t.Fatal("roles share PID")
		}
		seen[pid] = true
	}
	result, err := lab.Quick20(ctx, c.Gateways())
	if err != nil || result.Admitted != 3 || result.Queued != 17 || !result.RetryStable || originCount(t, c) != 3 {
		t.Fatal("separate-process Quick20 failed")
	}
	// The public Gateway must not expose origin metrics or Admin endpoints.
	for _, path := range []string{"/__lab/count", "/api/admin/v1/users"} {
		code, _, _ := get(t, client(), "GET", c.gateways[0]+path, nil)
		if code != 404 {
			t.Fatal("private route exposed")
		}
	}
	coord := c.children[1]
	if coord.cmd.Process.Kill() != nil {
		t.Fatal("fault injection failed")
	}
	select {
	case <-coord.done:
	case <-time.After(5 * time.Second):
		t.Fatal("Coordinator did not exit")
	}
	req, _ := http.NewRequest("POST", c.gateways[0]+"/_wr/v1/tickets", bytes.NewBufferString(`{"target":"/shop"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "process-after-coordinator-death")
	resp, err := client().Do(req)
	if err != nil {
		t.Fatal("Gateway unavailable after Coordinator death")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 503 {
		t.Fatal("new admission did not fail closed")
	}
	code, _, _ := get(t, client(), "POST", c.gateways[1]+"/shop", nil)
	if code != 429 || originCount(t, c) != 3 {
		t.Fatal("Coordinator loss bypass")
	}
	t.Logf("four distinct child PIDs=%v; real Quick20=3 admitted/17 queued; Coordinator SIGKILL yields join 503, unsafe 429, origin count remains 3", c.Summary().ChildPIDs)
}

func TestProcessBrowserCrossGatewayAndRestart(t *testing.T) {
	binary := buildLab(t)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	c, err := Start(ctx, binary)
	if err != nil {
		t.Fatal(err)
	}
	defer assertClosed(t, c, false)
	var selected atomic.Int32
	var targets atomic.Value
	targets.Store(c.Gateways())
	front := httptest.NewServer(&httputil.ReverseProxy{Rewrite: func(p *httputil.ProxyRequest) {
		u, _ := url.Parse(targets.Load().([]string)[selected.Load()])
		p.SetURL(u)
		p.Out.Host = p.In.Host
	}})
	defer front.Close()
	httpClient := client()
	httpClient.Jar, _ = cookiejar.New(nil)
	code, _, h := get(t, httpClient, "GET", front.URL+"/shop/browser?tab=one", map[string]string{"Accept": "text/html"})
	if code != 303 {
		t.Fatal("browser join")
	}
	waiting := h.Get("Location")
	selected.Store(1)
	code, _, _ = get(t, httpClient, "GET", front.URL+waiting, nil)
	if code != 200 {
		t.Fatal("cross-process return invalid")
	}
	u, _ := url.Parse(front.URL)
	var ticket string
	for _, cookie := range httpClient.Jar.Cookies(u) {
		if strings.HasPrefix(cookie.Name, "wr_dev_q_") {
			ticket = cookie.Value
		}
	}
	// Restart Gateway 2 with the same private configuration through new pipes.
	// The parent retains only Gateway material, never the admission private key.
	old := c.children[3]
	old.close()
	if old.cmd.ProcessState == nil || !old.cmd.ProcessState.Success() {
		t.Fatal("old Gateway failed during graceful shutdown")
	}
	// Decode the replacement input from explicitly retained gateway configuration.
	replacement, err := startChild(ctx, binary, "gateway", c.gatewayConfig)
	if err != nil {
		t.Fatal(err)
	}
	c.children[3] = replacement
	next := c.Gateways()
	next[1] = replacement.ready.URL
	c.gateways = next
	targets.Store(next)
	code, _, _ = get(t, httpClient, "GET", front.URL+waiting, nil)
	if code != 200 {
		t.Fatal("Gateway restart lost return binding")
	}
	for _, cookie := range httpClient.Jar.Cookies(u) {
		if strings.HasPrefix(cookie.Name, "wr_dev_q_") && cookie.Value != ticket {
			t.Fatal("Gateway restart allocated new ticket")
		}
	}
	statusPath := "/_wr/v1/rooms/" + lab.Room + "/status"
	deadline := time.Now().Add(5 * time.Second)
	for {
		code, b, _ := get(t, httpClient, "GET", front.URL+statusPath, nil)
		var s struct{ State string }
		_ = json.Unmarshal(b, &s)
		if code == 200 && s.State == "ready" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("not ready")
		}
		time.Sleep(20 * time.Millisecond)
	}
	parsed, _ := url.Parse(waiting)
	claim := "/_wr/v1/rooms/" + lab.Room + "/admissions?return=" + url.QueryEscape(parsed.Query().Get("return"))
	code, _, h = get(t, httpClient, "POST", front.URL+claim, map[string]string{"Origin": front.URL})
	if code != 303 || h.Get("Location") != "/shop/browser?tab=one" {
		t.Fatal("browser claim/return")
	}
	selected.Store(0)
	code, b, _ := get(t, httpClient, "GET", front.URL+h.Get("Location"), nil)
	var result struct{ PID int }
	_ = json.Unmarshal(b, &result)
	if code != 200 || result.PID != c.children[0].ready.PID || originCount(t, c) != 1 {
		t.Fatal("separate origin not reached")
	}
	// Closing the parent's control pipe must stop a child without a signal.
	_ = replacement.input.Close()
	select {
	case <-replacement.done:
	case <-time.After(5 * time.Second):
		t.Fatal("parent pipe EOF left child running")
	}
	t.Log("browser join -> other Gateway process -> Gateway replacement with same key -> claim -> separate origin; ticket preserved; parent pipe EOF exits child")
}
