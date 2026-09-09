// SPDX-License-Identifier: Apache-2.0
package trafficlab

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	valkey "github.com/valkey-io/valkey-go"
	"waiting-room/internal/control"
	"waiting-room/internal/lab"
	"waiting-room/internal/queue/valkeystore"
)

type response struct {
	status int
	body   []byte
	header http.Header
}
type observer struct {
	mu                   sync.Mutex
	times                []int64
	expected, unexpected int
	lastFailure          *Check
}

func (o *observer) request(ctx context.Context, c *http.Client, method, address string, body []byte, headers map[string]string, want int) (response, error) {
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, method, address, bytes.NewReader(body))
	if err != nil {
		return response{}, ErrRun
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := c.Do(req)
	out := response{}
	if err == nil {
		out.status = res.StatusCode
		out.header = res.Header
		out.body, err = io.ReadAll(io.LimitReader(res.Body, 32769))
		res.Body.Close()
		if len(out.body) > 32768 {
			err = ErrRun
		}
	}
	o.mu.Lock()
	o.times = append(o.times, time.Since(start).Milliseconds())
	if err != nil || out.status != want {
		o.unexpected++
		actual := fmt.Sprintf("HTTP %d", out.status)
		if err != nil {
			actual = "요청 연결 실패 또는 시간 초과"
		}
		o.lastFailure = &Check{"scenario-stage", fmt.Sprintf("HTTP %d", want), actual, false}
	} else if want >= 400 {
		o.expected++
	}
	o.mu.Unlock()
	if err != nil || out.status != want {
		return out, ErrRun
	}
	return out, nil
}
func (o *observer) copy(r *Report) {
	o.mu.Lock()
	defer o.mu.Unlock()
	r.Requests = len(o.times)
	r.ExpectedRejections = o.expected
	r.UnexpectedErrors = o.unexpected
	if r.State == "failed" || r.State == "interrupted" {
		if o.lastFailure != nil {
			r.Checks = append(r.Checks, *o.lastFailure)
		} else {
			r.Checks = append(r.Checks, Check{"scenario-stage", "모든 시나리오 조건 충족", "시나리오 완료 전 종료", false})
		}
	}
	if len(o.times) > 0 {
		values := append([]int64(nil), o.times...)
		sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
		r.P95MS = values[(len(values)-1)*95/100]
	}
}
func httpClient(jar http.CookieJar) *http.Client {
	return &http.Client{Jar: jar, Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil, MaxConnsPerHost: 16, MaxIdleConnsPerHost: 16, ResponseHeaderTimeout: 3 * time.Second}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

type visitor struct {
	index                          int
	kind, token, target, admission string
	sequence                       uint64
	client                         *http.Client
}

// NewExecutor binds database connectivity at deployment time. The public input
// contains only a preset and opaque job ID; callers cannot select any URL/key.
func NewExecutor(options valkey.ClientOption) Execute {
	slots := make(chan struct{}, 1)
	return func(ctx context.Context, in Input, emit func(Report) error) (report Report, err error) {
		report = NewReport(in.Preset)
		if Visitors(in.Preset) == 0 {
			return report, ErrRun
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			return report, ErrRun
		}
		ctx, cancel := context.WithTimeout(ctx, MaxDuration)
		defer cancel()
		o := &observer{}
		defer func() {
			now := time.Now().UTC()
			report.FinishedAt = &now
			report.DurationMS = now.Sub(report.StartedAt).Milliseconds()
			report.Stage = "finished"
			if err != nil {
				report.State = "failed"
				if ctx.Err() != nil {
					report.State = "interrupted"
				}
			} else {
				report.State = "passed"
			}
			o.copy(&report)
		}()
		progress := func(stage string) error {
			report.Stage = stage
			report.DurationMS = time.Since(report.StartedAt).Milliseconds()
			o.copy(&report)
			if emit != nil {
				return emit(report)
			}
			return nil
		}
		if err = progress("starting"); err != nil {
			return report, ErrRun
		}
		f, e := openFixture(ctx, options)
		if e != nil {
			return report, ErrRun
		}
		defer f.close()
		client := httpClient(nil)
		defer client.CloseIdleConnections()
		base := "/_wr/v1/rooms/" + lab.Room
		if _, e = o.request(ctx, client, "GET", f.gateways[0]+"/shop", nil, nil, 429); e != nil {
			return report, e
		}
		if f.reached.Load() != 0 {
			return report, ErrRun
		}
		visitors := make([]visitor, report.Visitors)
		defer func() {
			for i := range visitors {
				if visitors[i].client != nil {
					visitors[i].client.CloseIdleConnections()
				}
			}
		}()
		if err = progress("joining"); err != nil {
			return report, ErrRun
		}
		// Quick uses ordered mixed browser/app visitors. Smoke uses eight workers
		// and concurrent duplicate joins, comparing persisted sequence, not launch order.
		width := 1
		if in.Preset == "smoke-1k" {
			width = 8
		}
		for from := 0; from < len(visitors); from += width {
			var wg sync.WaitGroup
			failures := make(chan error, width)
			for i := from; i < min(from+width, len(visitors)); i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					v := visitor{index: i + 1, kind: "app", client: client}
					if in.Preset == "quick-20" && i%2 == 0 {
						v.kind = "browser"
						jar, _ := cookiejar.New(nil)
						v.client = httpClient(jar)
					}
					visitors[i] = v
					if e := join(ctx, o, f, &visitors[i], in.RunID, width > 1); e != nil {
						failures <- e
					}
				}(i)
			}
			wg.Wait()
			close(failures)
			if len(failures) > 0 {
				return report, ErrRun
			}
			report.Joined = min(from+width, len(visitors))
			f.expire(ctx)
			if report.Joined == len(visitors) || report.Joined%200 == 0 {
				if err = progress("joining"); err != nil {
					return report, ErrRun
				}
			}
		}
		report.Checks = append(report.Checks, Check{"join-retry", "방문자마다 티켓 하나", fmt.Sprintf("티켓 %d개 · 재시도 일치", report.Joined), true})
		if err = progress("checking"); err != nil {
			return report, ErrRun
		}
		if _, e = f.store.Configure(ctx, f.config, 1, control.Runtime{Revision: 2, Epoch: 1, Mode: "AUTO"}); e != nil {
			return report, ErrRun
		}
		promoted, e := f.store.Promote(ctx, 128)
		if e != nil || len(promoted.Tickets) != 3 {
			return report, ErrRun
		}
		sort.Slice(visitors, func(i, j int) bool { return visitors[i].sequence < visitors[j].sequence })
		for i := range visitors {
			v := &visitors[i]
			if v.sequence != uint64(i+1) {
				return report, ErrRun
			}
			want, state := 202, "queued"
			if i < 3 {
				want, state = 200, "ready"
			}
			headers := map[string]string{}
			if v.kind == "app" {
				headers["Authorization"] = "Bearer " + v.token
			}
			out, e := o.request(ctx, v.client, "GET", f.gateways[i%2]+base+"/status", nil, headers, want)
			if e != nil {
				return report, e
			}
			var data struct{ State string }
			if json.Unmarshal(out.body, &data) != nil || data.State != state {
				return report, ErrRun
			}
			if i < 3 {
				if e = claim(ctx, o, f, v, base); e != nil {
					return report, e
				}
				report.Admitted++
			} else {
				report.Queued++
			}
			if i < 20 {
				actual := state
				if i < 3 {
					actual = "admitted"
				}
				report.Timeline = append(report.Timeline, Event{v.index, v.kind, v.sequence, actual, actual, true, time.Now().UTC()})
			}
		}
		report.Checks = append(report.Checks, Check{"fifo", "대기 순번 1~3 입장", fmt.Sprintf("%d명 입장 · %d명 대기", report.Admitted, report.Queued), true})
		if _, e = f.store.Promote(ctx, 128); e != nil {
			return report, ErrRun
		}
		metrics, e := f.store.Metrics(ctx)
		if e != nil || metrics.Metrics == nil || metrics.Metrics.Leases != 3 || metrics.Metrics.Ready != 0 || metrics.Metrics.Waiting != report.Visitors-3 || metrics.Metrics.Rate != 3 {
			return report, ErrRun
		}
		report.Checks = append(report.Checks, Check{"lease-cap", "입장 3명 · 추가 입장 없음", "입장 3명 · 예약 3건", true}, Check{"claim-retry", "재시도에서 같은 입장권 반환", "첫 3명 입장권 동일", true})
		if _, e = o.request(ctx, client, "POST", f.gateways[0]+base+"/admissions", nil, map[string]string{"Authorization": "Bearer " + visitors[3].token}, 409); e != nil {
			return report, e
		}
		report.Checks = append(report.Checks, Check{"early-claim", "대기 중인 방문자의 입장 거절", "HTTP 409", true})
		if f.reached.Load() != 3 || f.leaked.Load() {
			return report, ErrRun
		}
		report.Checks = append(report.Checks, Check{"origin-protection", "입장한 3명만 원본 요청 · 인증값 제거", "샘플 원본 요청 3건 · 인증값 유출 없음", true})
		f.coord.close()
		if _, e = o.request(ctx, client, "POST", f.gateways[0]+"/_wr/v1/tickets", []byte(`{"target":"/shop"}`), map[string]string{"Content-Type": "application/json", "Idempotency-Key": "traffic-failure-" + in.RunID}, 503); e != nil {
			return report, e
		}
		if _, e = o.request(ctx, visitors[0].client, "GET", f.gateways[1]+"/shop", nil, admissionHeaders(visitors[0]), 200); e != nil {
			return report, e
		}
		if f.reached.Load() != 4 || f.leaked.Load() {
			return report, ErrRun
		}
		report.Checks = append(report.Checks, Check{"coordinator-loss", "신규 입장 요청 차단 · 기존 입장권 사용 가능", "HTTP 503 / HTTP 200", true})
		return report, nil
	}
}
func admissionHeaders(v visitor) map[string]string {
	if v.kind == "app" {
		return map[string]string{"X-Waiting-Room-Admission": v.admission}
	}
	return nil
}
func join(ctx context.Context, o *observer, f *fixture, v *visitor, run string, concurrent bool) error {
	if v.kind == "browser" {
		nav := map[string]string{"Accept": "text/html"}
		page, err := o.request(ctx, v.client, "GET", f.gateways[0]+"/shop/first", nil, nav, 200)
		if err != nil || !bytes.Contains(page.body, []byte(`data-bootstrap="true"`)) {
			return ErrRun
		}
		// The real browser establishes its HttpOnly intent before allocating a
		// queue ticket. Exercise both cookie round trips in the HTTP preset too.
		for _, confirm := range []bool{false, true} {
			body, _ := json.Marshal(map[string]any{"target": "/shop/first", "confirm": confirm})
			headers := map[string]string{"Content-Type": "application/json", "Origin": f.gateways[0]}
			if _, err := o.request(ctx, v.client, "POST", f.gateways[0]+"/_wr/v1/rooms/"+lab.Room+"/browser-prepare", body, headers, 204); err != nil {
				return err
			}
		}
		out, err := o.request(ctx, v.client, "GET", f.gateways[0]+"/shop/first", nil, nav, 303)
		if err != nil {
			return err
		}
		v.target = out.header.Get("Location")
		if !strings.HasPrefix(v.target, "/_wr/wait/"+lab.Room+"?") {
			return ErrRun
		}
		u, _ := url.Parse(f.gateways[0])
		for _, cookie := range v.client.Jar.Cookies(u) {
			if cookie.Name == "wr_dev_q_"+lab.Room {
				v.token = cookie.Value
			}
		}
		if len(v.token) != 43 {
			return ErrRun
		}
		for range 2 {
			page, e := o.request(ctx, v.client, "GET", f.gateways[0]+v.target, nil, nav, 200)
			if e != nil || !bytes.Contains(page.body, []byte(`data-template="calm"`)) || bytes.Contains(page.body, []byte(v.token)) {
				return ErrRun
			}
		}
		if _, e := o.request(ctx, v.client, "GET", f.gateways[0]+"/shop/second", nil, nav, 303); e != nil {
			return e
		}
		for _, cookie := range v.client.Jar.Cookies(u) {
			if cookie.Name == "wr_dev_q_"+lab.Room && cookie.Value != v.token {
				return ErrRun
			}
		}
	} else {
		headers := map[string]string{"Content-Type": "application/json", "Idempotency-Key": fmt.Sprintf("traffic-%s-%d", run, v.index)}
		var outputs [2]response
		var errs [2]error
		request := func(i int) {
			outputs[i], errs[i] = o.request(ctx, v.client, "POST", f.gateways[i]+"/_wr/v1/tickets", []byte(`{"target":"/shop"}`), headers, 202)
		}
		if concurrent {
			var wg sync.WaitGroup
			for i := range 2 {
				wg.Add(1)
				go func(i int) { defer wg.Done(); request(i) }(i)
			}
			wg.Wait()
		} else {
			request(0)
			request(1)
		}
		if errs[0] != nil || errs[1] != nil || !bytes.Equal(outputs[0].body, outputs[1].body) {
			return ErrRun
		}
		var data struct{ TicketToken string }
		if json.Unmarshal(outputs[0].body, &data) != nil || len(data.TicketToken) != 43 {
			return ErrRun
		}
		v.token = data.TicketToken
	}
	result, err := f.store.Status(ctx, valkeystore.Hash(v.token))
	if err != nil || result.Ticket == nil {
		return ErrRun
	}
	v.sequence = result.Ticket.Sequence
	return nil
}
func claim(ctx context.Context, o *observer, f *fixture, v *visitor, base string) error {
	if v.kind == "browser" {
		u, _ := url.Parse(v.target)
		target := base + "/admissions?return=" + url.QueryEscape(u.Query().Get("return"))
		headers := map[string]string{"Origin": f.gateways[0]}
		first, e := o.request(ctx, v.client, "POST", f.gateways[0]+target, nil, headers, 303)
		if e != nil || first.header.Get("Location") != "/shop/first" {
			return ErrRun
		}
		second, e := o.request(ctx, v.client, "POST", f.gateways[0]+target, nil, headers, 303)
		if e != nil || first.header.Get("Set-Cookie") != second.header.Get("Set-Cookie") {
			return ErrRun
		}
	} else {
		headers := map[string]string{"Authorization": "Bearer " + v.token}
		var first, second response
		var a, b error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			first, a = o.request(ctx, v.client, "POST", f.gateways[0]+base+"/admissions", nil, headers, 200)
		}()
		go func() {
			defer wg.Done()
			second, b = o.request(ctx, v.client, "POST", f.gateways[1]+base+"/admissions", nil, headers, 200)
		}()
		wg.Wait()
		if a != nil || b != nil || !bytes.Equal(first.body, second.body) {
			return ErrRun
		}
		var data struct{ AdmissionToken string }
		if json.Unmarshal(first.body, &data) != nil || data.AdmissionToken == "" {
			return ErrRun
		}
		v.admission = data.AdmissionToken
	}
	_, err := o.request(ctx, v.client, "GET", f.gateways[1]+"/shop", nil, admissionHeaders(*v), 200)
	return err
}
