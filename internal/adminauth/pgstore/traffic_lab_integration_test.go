//go:build integration

// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"waiting-room/internal/adminauth"
	"waiting-room/internal/trafficlab"
)

func trafficFixture(t *testing.T) (fixture, *TrafficService, Grant) {
	f, c, g := controlFixture(t)
	execSQL(t, f.pool, Migration010)
	execSQL(t, f.pool, Migration012)
	s := &TrafficService{control: c, execute: func(_ context.Context, in trafficlab.Input, emit func(trafficlab.Report) error) (trafficlab.Report, error) {
		r := trafficlab.NewReport(in.Preset)
		_ = emit(r)
		r.State = "failed"
		r.Stage = "finished"
		at := time.Now().UTC()
		r.FinishedAt = &at
		return r, trafficlab.ErrRun
	}}
	return f, s, g
}
func labRequest(g Grant, key string) *http.Request {
	r := sessionRequest(g)
	r.Method = "POST"
	r.Header.Set("Idempotency-Key", key)
	return r
}
func startLab(t *testing.T, s *TrafficService, g Grant, key string) LabRun {
	t.Helper()
	out, err := s.Start(context.Background(), g.SessionToken(), labRequest(g, key), []byte(`{"preset":"quick-20"}`))
	if err != nil || out.Status != 202 {
		t.Fatal("start", out.Status, err)
	}
	var r LabRun
	if json.Unmarshal(out.Body, &r) != nil {
		t.Fatal("start JSON")
	}
	return r
}
func TestTrafficLabRolesCSRFStrictInputAndConcurrentSlot(t *testing.T) {
	f, s, g := trafficFixture(t)
	ctx := context.Background()
	r := labRequest(g, "traffic-auth-role-key")
	for _, bad := range []string{`{}`, `{"preset":"quick-20","url":"https://production.test"}`, `{"preset":"quick-20","preset":"smoke-1k"}`, `{"Preset":"quick-20"}`, `{"preset":"100k"}`} {
		out, err := s.Start(ctx, g.SessionToken(), labRequest(g, "invalid-input-"+fmt.Sprint(len(bad))), []byte(bad))
		if err != nil || out.Status != 400 {
			t.Fatal("bad input accepted", err, out.Status)
		}
	}
	execSQL(t, f.pool, "UPDATE auth_accounts SET role='viewer'")
	if _, err := s.Start(ctx, g.SessionToken(), r, []byte(`{"preset":"quick-20"}`)); !errors.Is(err, adminauth.ErrForbidden) {
		t.Fatal("viewer ran traffic", err)
	}
	if out, err := s.List(ctx, g.SessionToken(), ""); err != nil || out.Status != 200 {
		t.Fatal("viewer read", err)
	}
	execSQL(t, f.pool, "UPDATE auth_accounts SET role='operator'")
	r.Header.Del("X-CSRF-Token")
	if _, err := s.Start(ctx, g.SessionToken(), r, []byte(`{"preset":"quick-20"}`)); !errors.Is(err, adminauth.ErrForbidden) {
		t.Fatal("CSRF bypass", err)
	}
	var wg sync.WaitGroup
	var started, busy atomic.Int32
	for i := range 8 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			out, err := s.Start(ctx, g.SessionToken(), labRequest(g, fmt.Sprintf("traffic-concurrent-%d", i)), []byte(`{"preset":"quick-20"}`))
			if err != nil {
				t.Error(err)
			} else if out.Status == 202 {
				started.Add(1)
			} else if out.Status == 409 {
				busy.Add(1)
			} else {
				t.Error(out.Status)
			}
		}(i)
	}
	wg.Wait()
	if started.Load() != 1 || busy.Load() != 7 {
		t.Fatal("active slot", started.Load(), busy.Load())
	}
	var revision int
	f.pool.QueryRow(ctx, "SELECT revision FROM control_config").Scan(&revision)
	if revision != 0 {
		t.Fatal("lab changed production config")
	}
}
func TestTrafficLabRetryPersistenceFailureAndAuditOnce(t *testing.T) {
	f, s, g := trafficFixture(t)
	ctx := context.Background()
	run := startLab(t, s, g, "traffic-persist-key")
	out, err := s.Start(ctx, g.SessionToken(), labRequest(g, "traffic-persist-key"), []byte(`{"preset":"quick-20"}`))
	if err != nil || !out.Replay {
		t.Fatal("lost-response replay", err)
	}
	var replay LabRun
	json.Unmarshal(out.Body, &replay)
	if replay.ID != run.ID {
		t.Fatal("replay ran twice")
	}
	claimed, err := s.claim(ctx)
	if err != nil || claimed.ID != run.ID {
		t.Fatal("claim", err)
	}
	s.run(ctx, claimed)
	restarted := &TrafficService{s.control, s.execute}
	out, err = restarted.List(ctx, g.SessionToken(), run.ID)
	var final LabRun
	json.Unmarshal(out.Body, &final)
	if err != nil || final.State != "failed" || final.Report == nil || final.Report.State != "failed" {
		t.Fatal("failed evidence lost or misreported", err, final.State)
	}
	if err = s.finish(ctx, run.ID, *final.Report); err != nil {
		t.Fatal(err)
	}
	var count int
	f.pool.QueryRow(ctx, "SELECT count(*) FROM control_audit WHERE action='lab.result'").Scan(&count)
	if count != 1 {
		t.Fatal("duplicate terminal audit", count)
	}
	audit, _ := s.control.Audit(ctx, g.SessionToken(), 0)
	raw, _ := json.Marshal(audit)
	for _, secret := range []string{g.SessionToken(), g.CSRFToken(), "traffic-persist-key", "production.test"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("audit leak")
		}
	}
}
func TestTrafficLabCancellationAndStaleWorkerAreNotReplayed(t *testing.T) {
	f, s, g := trafficFixture(t)
	ctx := context.Background()
	run := startLab(t, s, g, "traffic-cancellation-key")
	entered := make(chan struct{})
	var calls atomic.Int32
	s.execute = func(ctx context.Context, in trafficlab.Input, emit func(trafficlab.Report) error) (trafficlab.Report, error) {
		calls.Add(1)
		close(entered)
		r := trafficlab.NewReport(in.Preset)
		if err := emit(r); err != nil {
			return r, err
		}
		<-ctx.Done()
		return r, ctx.Err()
	}
	claimed, _ := s.claim(ctx)
	done := make(chan struct{})
	go func() { defer close(done); s.run(ctx, claimed) }()
	<-entered
	if out, err := s.Cancel(ctx, g.SessionToken(), run.ID, labRequest(g, "traffic-cancel-request"), []byte("{ \n }")); err != nil || out.Status != 202 {
		t.Fatal(err, out.Status)
	}
	select {
	case <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("executor not cancelled")
	}
	var state string
	f.pool.QueryRow(ctx, "SELECT state FROM traffic_lab_runs WHERE id=$1", run.ID).Scan(&state)
	if state != "cancelled" {
		t.Fatal(state)
	}
	next := startLab(t, s, g, "traffic-expired-worker")
	_, _ = s.claim(ctx)
	partial := trafficlab.NewReport(next.Preset)
	partial.Stage = "joining"
	partial.Joined = 8
	partial.Requests = 16
	raw, _ := json.Marshal(partial)
	execSQL(t, f.pool, "UPDATE traffic_lab_runs SET report=$1 WHERE id=$2", raw, next.ID)
	execSQL(t, f.pool, "UPDATE traffic_lab_runs SET deadline_at=clock_timestamp()-interval '1 second' WHERE id=$1", next.ID)
	if err := s.reap(ctx); err != nil {
		t.Fatal(err)
	}
	f.pool.QueryRow(ctx, "SELECT state FROM traffic_lab_runs WHERE id=$1", next.ID).Scan(&state)
	if state != "interrupted" || calls.Load() != 1 {
		t.Fatal("stale work replayed", state, calls.Load())
	}
	stored, err := scanLab(f.pool.QueryRow(ctx, "SELECT "+labColumns+" FROM traffic_lab_runs WHERE id=$1", next.ID))
	if err != nil || stored.Report == nil || stored.Report.Joined != 8 || stored.Report.Requests != 16 {
		t.Fatal("interrupted partial evidence lost", err)
	}
	if claimed, err := s.claim(ctx); err != nil || claimed.ID != "" {
		t.Fatal("terminal run reclaimed", err)
	}
}
func TestTrafficLabTerminalAuditFailureRollsBackResult(t *testing.T) {
	f, s, g := trafficFixture(t)
	ctx := context.Background()
	run := startLab(t, s, g, "traffic-audit-failure-key")
	_, _ = s.claim(ctx)
	execSQL(t, f.pool, "CREATE FUNCTION reject_lab_result() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='lab.result' THEN RAISE EXCEPTION 'fixture'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_lab_result BEFORE INSERT ON control_audit FOR EACH ROW EXECUTE FUNCTION reject_lab_result()")
	report := trafficlab.NewReport(run.Preset)
	report.State = "failed"
	report.Stage = "finished"
	at := time.Now().UTC()
	report.FinishedAt = &at
	if err := s.finish(ctx, run.ID, report); err == nil {
		t.Fatal("missing audit accepted")
	}
	var state string
	f.pool.QueryRow(ctx, "SELECT state FROM traffic_lab_runs WHERE id=$1", run.ID).Scan(&state)
	if state != "running" {
		t.Fatal("partial terminal commit", state)
	}
}
