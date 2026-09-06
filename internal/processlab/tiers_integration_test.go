//go:build integration && tiers

// SPDX-License-Identifier: Apache-2.0
package processlab

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	valkey "github.com/valkey-io/valkey-go"
	"waiting-room/internal/lab"
	"waiting-room/internal/queue/model"
	"waiting-room/internal/queue/valkeystore"
)

func TestProcessHTTPVisitorTiers(t *testing.T) {
	binary := buildLab(t)
	for _, n := range []int{1000, 2000, 5000, 10000} {
		if !t.Run(strconv.Itoa(n), func(t *testing.T) { httpVisitorTier(t, binary, n) }) {
			break
		}
	}
}

func parallelHTTP(n int, work func(int)) {
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				work(i)
			}
		}()
	}
	for i := 0; i < n; i++ {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
}

// Error text intentionally excludes credential-bearing URL, headers and body.
func tierRequest(ctx context.Context, h *http.Client, method, address string, body []byte, headers map[string]string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, address, bytes.NewReader(body))
	if err != nil {
		return 0, nil, ErrRole
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	r, err := h.Do(req)
	if err != nil {
		return 0, nil, ErrRole
	}
	defer r.Body.Close()
	b, err := io.ReadAll(io.LimitReader(r.Body, 16385))
	if err != nil || len(b) > 16384 {
		return r.StatusCode, nil, ErrRole
	}
	if strings.HasPrefix(req.URL.Path, "/_wr/v1/") && r.Header.Get("Cache-Control") != "no-store" {
		return r.StatusCode, nil, ErrRole
	}
	return r.StatusCode, b, nil
}

func httpResources(t *testing.T, v valkey.Client) map[string]int64 {
	t.Helper()
	raw, err := v.Do(context.Background(), v.B().Info().Section("memory", "stats").Build()).ToString()
	if err != nil {
		t.Fatal("resource read failed")
	}
	m := map[string]int64{}
	for _, line := range strings.Split(raw, "\r\n") {
		key, value, ok := strings.Cut(line, ":")
		if ok {
			n, e := strconv.ParseInt(value, 10, 64)
			if e == nil {
				m[key] = n
			}
		}
	}
	for _, key := range []string{"used_memory", "maxmemory", "evicted_keys"} {
		if _, ok := m[key]; !ok {
			t.Fatal("resource field unavailable", key)
		}
	}
	return m
}

func httpVisitorTier(t *testing.T, binary string, n int) {
	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	defer cancel()
	v, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{"127.0.0.1:16379"}, ForceSingleClient: true, DisableCache: true, DisableRetry: true})
	if err != nil {
		t.Fatal("Valkey unavailable")
	}
	defer v.Close()
	before := httpResources(t, v)
	if before["maxmemory"] <= 0 || before["used_memory"]+int64(n)*8192 > before["maxmemory"]*7/10 {
		t.Fatal("NO-GO_PREFLIGHT: fixture memory envelope exceeds budget")
	}
	c, err := Start(ctx, binary)
	if err != nil {
		t.Fatal(err)
	}
	killed := false
	defer func() {
		if t.Failed() {
			// Fixed metadata fields only: never log config, credentials or ticket records.
			diagnostic, stop := context.WithTimeout(context.Background(), time.Second)
			defer stop()
			for _, scope := range []string{"room", "installation"} {
				values := map[string]string{}
				for _, field := range []string{"schema", "mode", "dirty", "clock", "visitors", "idems"} {
					value, e := v.Do(diagnostic, v.B().Hget().Key(c.namespace+"{"+scope+":1}:meta").Field(field).Build()).ToString()
					if e != nil {
						value = "unavailable"
					}
					values[field] = value
				}
				t.Logf("HTTP_TIER_FAILURE_METADATA scope=%s values=%v", scope, values)
			}
		}
		assertClosed(t, c, killed)
		// Only this random fixture's eleven known keys; no scans, globs or volume removal.
		if !regexp.MustCompile(`^wr:lab:process-[a-f0-9]{32}$`).MatchString(c.namespace) {
			t.Error("invalid fixture ownership")
			return
		}
		keys := []string{}
		for _, suffix := range []string{"meta", "tickets", "waiting", "expiry", "leases", "rate", "idempotency", "idem-expiry"} {
			keys = append(keys, c.namespace+"{room:1}:"+suffix)
		}
		for _, suffix := range []string{"meta", "visitors", "idempotency"} {
			keys = append(keys, c.namespace+"{installation:1}:"+suffix)
		}
		if v.Do(context.Background(), v.B().Del().Key(keys...).Build()).Error() != nil {
			t.Error("owned fixture cleanup failed")
		}
	}()
	transport := &http.Transport{Proxy: nil, MaxConnsPerHost: 32, MaxIdleConnsPerHost: 32, MaxIdleConns: 64, ResponseHeaderTimeout: 3 * time.Second}
	defer transport.CloseIdleConnections()
	h := &http.Client{Transport: transport, Timeout: 4 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	gateways := c.Gateways()
	base := "/_wr/v1/rooms/" + lab.Room
	tokens := make([]string, n)
	responses := make([][]byte, n)
	joinMS := make([]float64, n)
	statusMS := make([]float64, n)
	ready := make([]bool, n)
	started := time.Now()
	headers := func(i int) map[string]string {
		return map[string]string{"Content-Type": "application/json", "Idempotency-Key": fmt.Sprintf("http-tier-visitor-%08d", i)}
	}
	var joinFailures atomic.Int64
	parallelHTTP(n, func(i int) {
		start := time.Now()
		code, b, e := tierRequest(ctx, h, "POST", gateways[i%2]+"/_wr/v1/tickets", []byte(`{"target":"/shop"}`), headers(i))
		joinMS[i] = float64(time.Since(start).Microseconds()) / 1000
		var data struct{ TicketToken string }
		if e != nil || code != 202 || json.Unmarshal(b, &data) != nil || len(data.TicketToken) != 43 {
			if joinFailures.Add(1) <= 8 {
				var problem struct{ Code string }
				_ = json.Unmarshal(b, &problem)
				category := "other"
				switch problem.Code {
				case "CONFIG_UNAVAILABLE", "QUEUE_UNAVAILABLE", "QUEUE_CAPACITY_EXCEEDED":
					category = problem.Code
				}
				t.Errorf("join[%d]: HTTP %d category=%s transportFailed=%v deadlineReached=%v", i, code, category, e != nil, ctx.Err() != nil)
			}
			return
		}
		tokens[i] = data.TicketToken
		responses[i] = b
	})
	if failed := joinFailures.Load(); failed > 0 {
		t.Logf("HTTP_TIER_JOIN_FAILURES count=%d", failed)
	}
	if t.Failed() {
		return
	}
	unique := map[string]bool{}
	for _, token := range tokens {
		if unique[token] {
			t.Fatal("duplicate ticket")
		}
		unique[token] = true
	}
	// One bounded scheduler wait, not an early-poll workload. Poll scheduling is not qualified here.
	select {
	case <-ctx.Done():
		t.Fatal("tier timeout")
	case <-time.After(75 * time.Millisecond):
	}
	parallelHTTP(n, func(i int) {
		start := time.Now()
		code, b, e := tierRequest(ctx, h, "GET", gateways[(i+1)%2]+base+"/status", nil, map[string]string{"Authorization": "Bearer " + tokens[i]})
		statusMS[i] = float64(time.Since(start).Microseconds()) / 1000
		var state struct{ State string }
		if e != nil || json.Unmarshal(b, &state) != nil {
			t.Errorf("status[%d]: HTTP %d transportFailed=%v deadlineReached=%v", i, code, e != nil, ctx.Err() != nil)
			return
		}
		if code == 200 && state.State == "ready" {
			ready[i] = true
		} else if code != 202 || state.State != "queued" {
			t.Errorf("status[%d]: HTTP %d state %s", i, code, state.State)
		}
	})
	if t.Failed() {
		return
	}
	parallelHTTP(n/10, func(i int) {
		index := i * 10
		code, b, e := tierRequest(ctx, h, "POST", gateways[(index+1)%2]+"/_wr/v1/tickets", []byte(`{"target":"/shop"}`), headers(index))
		if e != nil || code != 202 || !bytes.Equal(b, responses[index]) {
			t.Errorf("join replay[%d]: HTTP %d transportFailed=%v bodyChanged=%v deadlineReached=%v", index, code, e != nil, !bytes.Equal(b, responses[index]), ctx.Err() != nil)
		}
	})
	if t.Failed() {
		return
	}
	admitted := 0
	admissionTokens := []string{}
	seenSeq := map[uint64]bool{}
	for i, isReady := range ready {
		if !isReady {
			continue
		}
		raw, e := v.Do(ctx, v.B().Hget().Key(c.namespace+"{room:1}:tickets").Field(valkeystore.Hash(tokens[i])).Build()).ToString()
		var ticket model.Ticket
		if e != nil || json.Unmarshal([]byte(raw), &ticket) != nil || ticket.Sequence < 1 || ticket.Sequence > 3 || seenSeq[ticket.Sequence] {
			t.Fatal("FIFO reservation mismatch")
		}
		seenSeq[ticket.Sequence] = true
		auth := map[string]string{"Authorization": "Bearer " + tokens[i]}
		code, b, e := tierRequest(ctx, h, "POST", gateways[i%2]+base+"/admissions", nil, auth)
		if e != nil || code != 200 {
			t.Fatal("claim failed")
		}
		code, retry, e := tierRequest(ctx, h, "POST", gateways[(i+1)%2]+base+"/admissions", nil, auth)
		if e != nil || code != 200 || !bytes.Equal(b, retry) {
			t.Fatal("cross-Gateway claim replay changed")
		}
		var data struct{ AdmissionToken string }
		if json.Unmarshal(b, &data) != nil || data.AdmissionToken == "" {
			t.Fatal("claim response invalid")
		}
		admissionTokens = append(admissionTokens, data.AdmissionToken)
		code, b, e = tierRequest(ctx, h, "GET", gateways[(i+1)%2]+"/shop", nil, map[string]string{"X-Waiting-Room-Admission": data.AdmissionToken})
		var origin struct{ PID int }
		if e != nil || code != 200 || json.Unmarshal(b, &origin) != nil || origin.PID != c.children[0].ready.PID {
			t.Fatal("origin pass failed")
		}
		admitted++
	}
	if admitted != 3 {
		t.Fatal("expected exactly three admissions", admitted)
	}
	parallelHTTP(128, func(i int) {
		code, _, e := tierRequest(ctx, h, "POST", gateways[i%2]+"/shop", []byte("fixture-not-replayed"), nil)
		if e != nil || code != 429 {
			t.Errorf("unsafe origin bypass[%d]: %d", i, code)
		}
	})
	overflow := 0
	if n == 10000 {
		overflow = 128
		parallelHTTP(overflow, func(i int) {
			code, b, e := tierRequest(ctx, h, "POST", gateways[i%2]+"/_wr/v1/tickets", []byte(`{"target":"/shop"}`), headers(n+i))
			var problem struct{ Code string }
			if e != nil || code != 503 || json.Unmarshal(b, &problem) != nil || problem.Code != "QUEUE_CAPACITY_EXCEEDED" {
				t.Errorf("capacity rejection[%d]: %d", i, code)
			}
		})
	}
	count, e := v.Do(ctx, v.B().Zcard().Key(c.namespace+"{installation:1}:visitors").Build()).ToInt64()
	if e != nil || count != int64(n) {
		t.Fatal("installation population mismatch")
	}
	if originCount(t, c) != 3 {
		t.Fatal("non-admitted request reached origin")
	}
	after := httpResources(t, v)
	if after["used_memory"] >= after["maxmemory"]*7/10 || after["evicted_keys"] != before["evicted_keys"] {
		t.Fatal("resource safety threshold")
	}
	if n == 10000 {
		if c.children[1].cmd.Process.Kill() != nil {
			t.Fatal("Coordinator fault failed")
		}
		killed = true
		select {
		case <-c.children[1].done:
		case <-ctx.Done():
			t.Fatal("Coordinator did not exit")
		}
		code, _, e := tierRequest(ctx, h, "POST", gateways[1]+"/_wr/v1/tickets", []byte(`{"target":"/shop"}`), headers(n+129))
		if e != nil || code != 503 {
			t.Fatal("new join after fault did not fail closed")
		}
		for _, token := range admissionTokens {
			code, _, e = tierRequest(ctx, h, "GET", gateways[0]+"/shop", nil, map[string]string{"X-Waiting-Room-Admission": token})
			if e != nil || code != 200 {
				t.Fatal("valid admission after fault blocked")
			}
		}
		if originCount(t, c) != 6 {
			t.Fatal("unexpected origin count after fault")
		}
	}
	sort.Float64s(joinMS)
	sort.Float64s(statusMS)
	summary := map[string]any{"scope": "local-app-http-four-child-processes", "visitors": n, "workers": 32, "joined": n, "statusReads": n, "retries": n / 10, "admitted": admitted, "queued": n - admitted, "expectedUnsafeRejections": 128, "expectedCapacityRejections": overflow, "coordinatorDeathChecked": killed, "joinP95ms": joinMS[n*95/100-1], "joinP99ms": joinMS[n*99/100-1], "statusP95ms": statusMS[n*95/100-1], "statusP99ms": statusMS[n*99/100-1], "elapsedSeconds": time.Since(started).Seconds(), "valkeyUsedBytes": after["used_memory"], "evictions": after["evicted_keys"] - before["evicted_keys"], "qualification": "NOT_RUN"}
	b, _ := json.Marshal(summary)
	t.Log("HTTP_TIER_RESULT " + string(b))
}
