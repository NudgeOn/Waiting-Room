// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"waiting-room/internal/adminauth"
	"waiting-room/internal/control"
	"waiting-room/internal/trafficlab"
)

//go:embed migrations/012_traffic_lab.sql
var Migration012 string
var LabRunID = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

type LabRun struct {
	ID         string             `json:"id"`
	Preset     string             `json:"preset"`
	State      string             `json:"state"`
	ActorID    string             `json:"actorId"`
	CreatedAt  time.Time          `json:"createdAt"`
	StartedAt  *time.Time         `json:"startedAt"`
	FinishedAt *time.Time         `json:"finishedAt"`
	DeadlineAt time.Time          `json:"deadlineAt"`
	Report     *trafficlab.Report `json:"report"`
}

const labColumns = "id,preset,state,actor_id,created_at,started_at,finished_at,deadline_at,report"

func scanLab(row interface{ Scan(...any) error }) (LabRun, error) {
	var run LabRun
	var raw []byte
	err := row.Scan(&run.ID, &run.Preset, &run.State, &run.ActorID, &run.CreatedAt, &run.StartedAt, &run.FinishedAt, &run.DeadlineAt, &raw)
	if err == nil && len(raw) > 0 {
		var report trafficlab.Report
		if json.Unmarshal(raw, &report) != nil || !report.Valid(run.Preset) {
			return run, adminauth.ErrAuthUnavailable
		}
		run.Report = &report
	}
	return run, err
}

type TrafficService struct {
	control *ControlService
	execute trafficlab.Execute
}

func NewTrafficService(s *Store, origin string, execute trafficlab.Execute) (*TrafficService, error) {
	c, err := NewControlService(s, origin)
	if err != nil || execute == nil {
		return nil, adminauth.ErrAuthUnavailable
	}
	return &TrafficService{c, execute}, nil
}
func (s *TrafficService) List(ctx context.Context, token, id string) (ControlReply, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, _, err := s.control.begin(ctx, token, adminauth.ReadLab, nil)
	if err != nil {
		return ControlReply{}, err
	}
	defer rollback(tx)
	if id != "" {
		run, err := scanLab(tx.QueryRow(ctx, "SELECT "+labColumns+" FROM traffic_lab_runs WHERE id=$1", id))
		if errors.Is(err, pgx.ErrNoRows) {
			return replyProblem(404, "NOT_FOUND"), nil
		}
		if err != nil {
			return ControlReply{}, adminauth.ErrAuthUnavailable
		}
		return jsonReply(200, run, ""), nil
	}
	rows, err := tx.Query(ctx, "SELECT "+labColumns+" FROM traffic_lab_runs ORDER BY created_at DESC,id DESC LIMIT 20")
	if err != nil {
		return ControlReply{}, adminauth.ErrAuthUnavailable
	}
	defer rows.Close()
	items := []LabRun{}
	for rows.Next() {
		run, e := scanLab(rows)
		if e != nil {
			return ControlReply{}, adminauth.ErrAuthUnavailable
		}
		items = append(items, run)
	}
	if rows.Err() != nil {
		return ControlReply{}, adminauth.ErrAuthUnavailable
	}
	return jsonReply(200, map[string]any{"items": items, "scope": "isolated-sample-origin", "maxDurationSeconds": 90}, ""), nil
}
func (s *TrafficService) Start(ctx context.Context, token string, r *http.Request, raw []byte) (ControlReply, error) {
	return s.control.command(ctx, token, r, raw, adminauth.RunLab, "traffic-lab", false, func(ctx context.Context, tx pgx.Tx, state sessionSnapshot, current control.Config) (commandResult, error) {
		out := commandResult{}
		var in struct {
			Preset string `json:"preset"`
		}
		if r.Method != "POST" || len(raw) > 1024 || control.DecodeExact(raw, &in) != nil || trafficlab.Visitors(in.Preset) == 0 {
			out.reply = replyProblem(400, "INVALID_REQUEST")
			return out, nil
		}
		var active bool
		if tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM traffic_lab_runs WHERE state IN ('queued','running','cancelling'))").Scan(&active) != nil {
			return out, adminauth.ErrAuthUnavailable
		}
		if active {
			out.reply = replyProblem(409, "LAB_BUSY")
			return out, nil
		}
		_, err := tx.Exec(ctx, "DELETE FROM traffic_lab_runs WHERE created_at<clock_timestamp()-interval '24 hours' AND state NOT IN ('queued','running','cancelling')")
		if err != nil {
			return out, adminauth.ErrAuthUnavailable
		}
		var count int
		if tx.QueryRow(ctx, "SELECT count(*) FROM traffic_lab_runs").Scan(&count) != nil {
			return out, adminauth.ErrAuthUnavailable
		}
		if count >= 500 {
			out.reply = replyProblem(429, "LAB_CAPACITY")
			return out, nil
		}
		id, _, err := adminauth.NewCSRFToken()
		if err != nil {
			return out, err
		}
		run, err := scanLab(tx.QueryRow(ctx, "INSERT INTO traffic_lab_runs(id,preset,state,actor_id,config_revision) VALUES($1,$2,'queued',$3,$4) RETURNING "+labColumns, id, in.Preset, state.account.ID, current.Revision))
		if err != nil {
			return out, adminauth.ErrAuthUnavailable
		}
		out.after = digest([]byte(id + ":" + in.Preset))
		out.reply = jsonReply(202, run, "")
		return out, nil
	})
}
func (s *TrafficService) Cancel(ctx context.Context, token, id string, r *http.Request, raw []byte) (ControlReply, error) {
	return s.control.command(ctx, token, r, raw, adminauth.RunLab, "traffic-lab:"+id+":cancel", false, func(ctx context.Context, tx pgx.Tx, _ sessionSnapshot, _ control.Config) (commandResult, error) {
		out := commandResult{}
		if r.Method != "POST" || control.DecodeExact(raw, &struct{}{}) != nil || !LabRunID.MatchString(id) {
			out.reply = replyProblem(400, "INVALID_REQUEST")
			return out, nil
		}
		// Keep the active slot until the worker has stopped the fixture.
		_, err := tx.Exec(ctx, "UPDATE traffic_lab_runs SET state='cancelling' WHERE id=$1 AND state IN ('queued','running')", id)
		if err != nil {
			return out, adminauth.ErrAuthUnavailable
		}
		run, err := scanLab(tx.QueryRow(ctx, "SELECT "+labColumns+" FROM traffic_lab_runs WHERE id=$1", id))
		if errors.Is(err, pgx.ErrNoRows) {
			out.reply = replyProblem(404, "NOT_FOUND")
			return out, nil
		}
		if err != nil {
			return out, adminauth.ErrAuthUnavailable
		}
		out.reply = jsonReply(202, run, "")
		return out, nil
	})
}

// Worker claims durable jobs once. A lost worker is recorded as interrupted;
// it is never automatically replayed against a possibly live fixture.
func (s *TrafficService) Worker(ctx context.Context) {
	for ctx.Err() == nil {
		_ = s.reap(ctx)
		run, err := s.claim(ctx)
		if err == nil && run.ID != "" {
			s.run(ctx, run)
			continue
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
func (s *TrafficService) claim(ctx context.Context) (LabRun, error) {
	tx, err := authTx(ctx, s.control.store)
	if err != nil {
		return LabRun{}, err
	}
	defer rollback(tx)
	row, err := scanLab(tx.QueryRow(ctx, "SELECT "+labColumns+" FROM traffic_lab_runs WHERE state='queued' AND deadline_at>clock_timestamp() ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1"))
	if errors.Is(err, pgx.ErrNoRows) {
		return LabRun{}, nil
	}
	if err != nil {
		return LabRun{}, err
	}
	_, err = tx.Exec(ctx, "UPDATE traffic_lab_runs SET state='running',started_at=clock_timestamp(),deadline_at=clock_timestamp()+interval '120 seconds' WHERE id=$1", row.ID)
	if err != nil {
		return LabRun{}, err
	}
	if tx.Commit(ctx) != nil {
		return LabRun{}, adminauth.ErrAuthUnavailable
	}
	return row, nil
}
func (s *TrafficService) reap(ctx context.Context) error {
	rows, err := s.control.store.pool.Query(ctx, "SELECT "+labColumns+" FROM traffic_lab_runs WHERE (state IN ('queued','running','cancelling') AND deadline_at<=clock_timestamp()) OR (state='cancelling' AND started_at IS NULL)")
	if err != nil {
		return err
	}
	var stale []LabRun
	for rows.Next() {
		r, e := scanLab(rows)
		if e != nil {
			rows.Close()
			return adminauth.ErrAuthUnavailable
		}
		stale = append(stale, r)
	}
	rows.Close()
	if rows.Err() != nil {
		return rows.Err()
	}
	for _, r := range stale {
		report := trafficlab.NewReport(r.Preset)
		if r.Report != nil {
			report = *r.Report
		}
		report.State = "interrupted"
		report.Stage = "finished"
		now := time.Now().UTC()
		report.FinishedAt = &now
		if err = s.finish(ctx, r.ID, report); err != nil {
			return err
		}
	}
	return nil
}
func (s *TrafficService) run(parent context.Context, run LabRun) {
	ctx, cancel := context.WithTimeout(parent, trafficlab.MaxDuration+5*time.Second)
	defer cancel()
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				var state string
				if s.control.store.pool.QueryRow(ctx, "SELECT state FROM traffic_lab_runs WHERE id=$1", run.ID).Scan(&state) != nil || state != "running" {
					cancel()
					return
				}
			}
		}
	}()
	emit := func(report trafficlab.Report) error {
		if !report.Valid(run.Preset) || report.State != "running" {
			return trafficlab.ErrRun
		}
		raw, _ := json.Marshal(report)
		if len(raw) > trafficlab.MaxReportBytes {
			return trafficlab.ErrRun
		}
		tag, err := s.control.store.pool.Exec(ctx, "UPDATE traffic_lab_runs SET report=$1 WHERE id=$2 AND state='running'", raw, run.ID)
		if err != nil || tag.RowsAffected() != 1 {
			return trafficlab.ErrRun
		}
		return nil
	}
	report, err := s.execute(ctx, trafficlab.Input{RunID: run.ID, Preset: run.Preset}, emit)
	close(done)
	<-stopped
	if !report.Valid(run.Preset) || report.State == "running" || (err != nil && report.State == "passed") {
		if !report.Valid(run.Preset) {
			report = trafficlab.NewReport(run.Preset)
		}
		report.State = "interrupted"
		report.Stage = "finished"
		now := time.Now().UTC()
		report.FinishedAt = &now
	}
	finish, c := context.WithTimeout(context.Background(), 5*time.Second)
	defer c()
	_ = s.finish(finish, run.ID, report)
}
func (s *TrafficService) finish(ctx context.Context, id string, report trafficlab.Report) error {
	tx, err := authTx(ctx, s.control.store)
	if err != nil {
		return err
	}
	defer rollback(tx)
	var state, preset string
	var revision int64
	if tx.QueryRow(ctx, "SELECT state,preset,config_revision FROM traffic_lab_runs WHERE id=$1 FOR UPDATE", id).Scan(&state, &preset, &revision) != nil {
		return adminauth.ErrAuthUnavailable
	}
	if state != "running" && state != "queued" && state != "cancelling" {
		return nil
	}
	if state == "cancelling" {
		report.State = "cancelled"
	}
	if !report.Valid(preset) || report.State == "running" || report.FinishedAt == nil {
		return adminauth.ErrAuthUnavailable
	}
	raw, _ := json.Marshal(report)
	if len(raw) > trafficlab.MaxReportBytes {
		return adminauth.ErrAuthUnavailable
	}
	if _, err = tx.Exec(ctx, "UPDATE traffic_lab_runs SET state=$1,report=$2,finished_at=clock_timestamp() WHERE id=$3", report.State, raw, id); err != nil {
		return adminauth.ErrAuthUnavailable
	}
	if _, err = tx.Exec(ctx, "INSERT INTO control_audit(actor_id,actor_role,action,target_id,before_digest,after_digest,result,request_id,revision) VALUES('system:traffic-lab','system','lab.result',$1,$2,$3,$4,$1,$5)", id, digest([]byte(preset)), digest(raw), report.State, revision); err != nil {
		return adminauth.ErrAuthUnavailable
	}
	if tx.Commit(ctx) != nil {
		return adminauth.ErrAuthUnavailable
	}
	return nil
}
