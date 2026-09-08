// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"runtime"
	"time"

	"github.com/jackc/pgx/v5"
	"waiting-room/internal/adminauth"
	"waiting-room/internal/control"
	"waiting-room/internal/installplan"
)

//go:embed migrations/011_installation_setup.sql
var Migration011 string

var ErrSetupConflict = errors.New("SETUP_CONFLICT")
var ErrSetupReview = errors.New("SETUP_REVIEW_REQUIRED")
var calibrationSlots = make(chan struct{}, 1)

type SetupService struct {
	store   *Store
	measure func(context.Context) (adminauth.Calibration, error)
}

func NewSetupService(s *Store) *SetupService { return &SetupService{s, adminauth.CalibratePassword} }

type SetupReview struct {
	Plan              installplan.SetupPlan `json:"plan"`
	PlanDigest        string                `json:"planDigest"`
	CalibrationDigest string                `json:"calibrationDigest"`
}
type SetupReport struct {
	SetupReview
	AppliedAt      time.Time        `json:"appliedAt"`
	ConfigRevision int64            `json:"configRevision"`
	Environment    SetupEnvironment `json:"environment"`
	CalibratedAt   time.Time        `json:"calibratedAt"`
}
type SetupEnvironment struct {
	OS                        string `json:"os"`
	Arch                      string `json:"arch"`
	AvailableCPUs             int    `json:"availableCpus"`
	GoParallelism             int    `json:"goParallelism"`
	Database                  string `json:"database"`
	DatabaseClockDifferenceMS int64  `json:"databaseClockDifferenceMs"`
	HostClock                 string `json:"hostClock"`
}
type SetupInspection struct {
	Input       installplan.Input `json:"input"`
	Environment SetupEnvironment  `json:"environment"`
	Report      *SetupReport      `json:"report"`
}

func (s *Store) PasswordIterations(ctx context.Context) (uint32, error) {
	var n uint32
	err := s.pool.QueryRow(ctx, "SELECT password_iterations FROM installation_setup WHERE singleton").Scan(&n)
	if err != nil || n < 2 || n > 10 {
		return 0, adminauth.ErrAuthUnavailable
	}
	return n, nil
}
func (s *Store) setupApplied(ctx context.Context) error {
	var applied bool
	if s.pool.QueryRow(ctx, "SELECT applied_at IS NOT NULL FROM installation_setup WHERE singleton").Scan(&applied) != nil {
		return adminauth.ErrAuthUnavailable
	}
	if !applied {
		return ErrSetupReview
	}
	return nil
}
func (s *SetupService) authorize(ctx context.Context, token string) ([32]byte, bootstrapSnapshot, error) {
	hash, ok := tokenHash(token)
	if !ok {
		return hash, bootstrapSnapshot{}, adminauth.ErrUnauthenticated
	}
	b, err := s.store.bootstrapSnapshot(ctx, hash)
	return hash, b, err
}
func (s *SetupService) environment(ctx context.Context) (SetupEnvironment, error) {
	start := time.Now()
	var now time.Time
	if s.store.pool.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now) != nil {
		return SetupEnvironment{}, adminauth.ErrAuthUnavailable
	}
	end := time.Now()
	offset := now.Sub(start.Add(end.Sub(start) / 2)).Milliseconds()
	if offset > 5000 || offset < -5000 || end.Sub(start) > 2*time.Second {
		return SetupEnvironment{}, adminauth.ErrAuthUnavailable
	}
	return SetupEnvironment{runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), runtime.GOMAXPROCS(0), "reachable", offset, "NOT_VERIFIED"}, nil
}
func (s *Store) setupReport(ctx context.Context) (*SetupReport, error) {
	var raw []byte
	if s.pool.QueryRow(ctx, "SELECT report FROM installation_setup WHERE singleton").Scan(&raw) != nil {
		return nil, adminauth.ErrAuthUnavailable
	}
	if len(raw) == 0 {
		return nil, nil
	}
	var report SetupReport
	if json.Unmarshal(raw, &report) != nil {
		return nil, adminauth.ErrAuthUnavailable
	}
	return &report, nil
}
func (s *SetupService) Inspect(ctx context.Context, token string) (SetupInspection, error) {
	_, b, err := s.authorize(ctx, token)
	if err != nil {
		return SetupInspection{}, err
	}
	env, err := s.environment(ctx)
	if err != nil {
		return SetupInspection{}, err
	}
	report, err := s.store.setupReport(ctx)
	if err != nil {
		return SetupInspection{}, err
	}
	in := installplan.Input{SchemaVersion: 1, Profile: "standard-10k", RegionID: "local", QueuePolicy: "fifo", ExpectedPeakVisitors: 1000, Limits: installplan.Limits{MaxActiveAdmissionLeases: 100, AdmissionsPerMinute: 60, AdmissionTTLSeconds: 900}, TOTP: installplan.TOTP{Mode: string(b.policy.Mode), Enabled: b.policy.TOTPEnabled}}
	if report != nil {
		in = report.Plan.Input
	}
	return SetupInspection{in, env, report}, nil
}

// Lock order matches bootstrap: policy -> bootstrap -> setup -> config/delivery.
// The token is rechecked after waiting. No KDF runs under these row locks.
func (s *SetupService) locked(ctx context.Context, tx pgx.Tx, hash [32]byte, b bootstrapSnapshot) error {
	var policy adminauth.AuthPolicy
	if tx.QueryRow(ctx, "SELECT mode,totp_enabled,version FROM auth_policy WHERE singleton FOR UPDATE").Scan(&policy.Mode, &policy.TOTPEnabled, &policy.Version) != nil {
		return adminauth.ErrAuthUnavailable
	}
	if _, err := tx.Exec(ctx, "SELECT singleton FROM auth_bootstrap WHERE singleton FOR UPDATE"); err != nil {
		return adminauth.ErrAuthUnavailable
	}
	var valid bool
	if tx.QueryRow(ctx, `SELECT NOT completed AND generation=$1 AND token_hash=$2 AND token_expires_at>clock_timestamp() AND NOT EXISTS(SELECT 1 FROM auth_accounts) FROM auth_bootstrap WHERE singleton`, b.generation, hash[:]).Scan(&valid) != nil {
		return adminauth.ErrAuthUnavailable
	}
	if !valid {
		return adminauth.ErrUnauthenticated
	}
	if policy != b.policy {
		return ErrSetupReview
	}
	return nil
}
func (s *SetupService) calibration(ctx context.Context, generation int64) (adminauth.Calibration, string, error) {
	var raw []byte
	var checksum string
	err := s.store.pool.QueryRow(ctx, `SELECT calibration,calibration_digest FROM installation_setup WHERE singleton AND calibration_generation=$1 AND calibrated_at>clock_timestamp()-interval '15 minutes' AND calibrated_at<=clock_timestamp()`, generation).Scan(&raw, &checksum)
	if errors.Is(err, pgx.ErrNoRows) {
		return adminauth.Calibration{}, "", ErrSetupReview
	}
	var c adminauth.Calibration
	if err != nil || json.Unmarshal(raw, &c) != nil {
		return c, "", adminauth.ErrAuthUnavailable
	}
	canonical, _ := json.Marshal(c)
	if digest(canonical) != checksum {
		return c, "", adminauth.ErrAuthUnavailable
	}
	return c, checksum, nil
}
func (s *SetupService) Calibrate(ctx context.Context, token string) (map[string]any, error) {
	hash, b, err := s.authorize(ctx, token)
	if err != nil {
		return nil, err
	}
	if report, err := s.store.setupReport(ctx); err != nil {
		return nil, err
	} else if report != nil {
		return nil, ErrSetupConflict
	}
	select {
	case calibrationSlots <- struct{}{}:
		defer func() { <-calibrationSlots }()
	default:
		return nil, adminauth.ErrAuthUnavailable
	}
	if _, err = s.environment(ctx); err != nil {
		return nil, err
	}
	c, err := s.measure(ctx)
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(c)
	// Hash canonical JSON, not PostgreSQL's jsonb whitespace/key ordering.
	checksum := digest(raw)
	tx, err := authTx(ctx, s.store)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	if err = s.locked(ctx, tx, hash, b); err != nil {
		return nil, err
	}
	tag, err := tx.Exec(ctx, `UPDATE installation_setup SET calibration=$1,calibration_digest=$2,calibration_generation=$3,calibrated_at=clock_timestamp() WHERE singleton AND applied_at IS NULL`, raw, checksum, b.generation)
	if err != nil {
		return nil, adminauth.ErrAuthUnavailable
	}
	if tag.RowsAffected() != 1 {
		return nil, ErrSetupConflict
	}
	if tx.Commit(ctx) != nil {
		return nil, adminauth.ErrAuthUnavailable
	}
	return map[string]any{"calibration": c, "calibrationDigest": checksum}, nil
}
func setupReview(in installplan.Input, c adminauth.Calibration, checksum string) (SetupReview, error) {
	p, err := installplan.BuildSetup(in, c)
	if err != nil {
		return SetupReview{}, err
	}
	raw, _ := json.Marshal(p)
	return SetupReview{p, digest(raw), checksum}, nil
}
func (s *SetupService) Plan(ctx context.Context, token string, in installplan.Input) (SetupReview, error) {
	_, b, err := s.authorize(ctx, token)
	if err != nil {
		return SetupReview{}, err
	}
	c, checksum, err := s.calibration(ctx, b.generation)
	if err != nil {
		return SetupReview{}, err
	}
	if b.policy.Mode == "forced_on" && in.TOTP.Mode != "forced_on" {
		return SetupReview{}, adminauth.ErrForbidden
	}
	return setupReview(in, c, checksum)
}
func (s *SetupService) Apply(ctx context.Context, token string, in installplan.SetupApply) (SetupReport, error) {
	hash, b, err := s.authorize(ctx, token)
	if err != nil {
		return SetupReport{}, err
	}
	environment, err := s.environment(ctx)
	if err != nil {
		return SetupReport{}, err
	}
	tx, err := authTx(ctx, s.store)
	if err != nil {
		return SetupReport{}, err
	}
	defer rollback(tx)
	if err = s.locked(ctx, tx, hash, b); err != nil {
		return SetupReport{}, err
	}
	var existing, raw []byte
	var checksum string
	var valid bool
	var calibratedAt *time.Time
	if tx.QueryRow(ctx, `SELECT report,calibration,COALESCE(calibration_digest,''),COALESCE(calibration_generation=$1 AND calibrated_at>clock_timestamp()-interval '15 minutes' AND calibrated_at<=clock_timestamp(),false),calibrated_at FROM installation_setup WHERE singleton FOR UPDATE`, b.generation).Scan(&existing, &raw, &checksum, &valid, &calibratedAt) != nil {
		return SetupReport{}, adminauth.ErrAuthUnavailable
	}
	if len(existing) > 0 {
		var report SetupReport
		if json.Unmarshal(existing, &report) != nil {
			return SetupReport{}, adminauth.ErrAuthUnavailable
		}
		if report.PlanDigest != in.PlanDigest || report.CalibrationDigest != in.CalibrationDigest || report.Plan.Input != in.Input {
			return SetupReport{}, ErrSetupConflict
		}
		return report, nil // Read-only retry; no second audit/revision or policy write.
	}
	if !valid || calibratedAt == nil || checksum != in.CalibrationDigest {
		return SetupReport{}, ErrSetupReview
	}
	var c adminauth.Calibration
	if json.Unmarshal(raw, &c) != nil {
		return SetupReport{}, adminauth.ErrAuthUnavailable
	}
	canonical, _ := json.Marshal(c)
	if digest(canonical) != checksum {
		return SetupReport{}, adminauth.ErrAuthUnavailable
	}
	review, err := setupReview(in.Input, c, checksum)
	if err != nil {
		return SetupReport{}, err
	}
	if review.PlanDigest != in.PlanDigest {
		return SetupReport{}, ErrSetupReview
	}
	if b.policy.Mode == "forced_on" && in.Input.TOTP.Mode != "forced_on" {
		return SetupReport{}, adminauth.ErrForbidden
	}
	var configRaw []byte
	var revision int64
	if tx.QueryRow(ctx, "SELECT revision,document FROM control_config WHERE singleton FOR UPDATE").Scan(&revision, &configRaw) != nil {
		return SetupReport{}, adminauth.ErrAuthUnavailable
	}
	var config control.Config
	if json.Unmarshal(configRaw, &config) != nil || len(config.Rooms) != 0 || revision != 0 {
		return SetupReport{}, ErrSetupConflict
	}
	config.Profile, config.RegionID = in.Input.Profile, in.Input.RegionID
	config.Revision = revision + 1
	updated := config.Bytes()
	if _, err = tx.Exec(ctx, "UPDATE control_config SET revision=$1,document=$2,updated_at=clock_timestamp() WHERE singleton", config.Revision, updated); err != nil {
		return SetupReport{}, adminauth.ErrAuthUnavailable
	}
	// Empty installation only: the worker will sign the newly applied region.
	if _, err = tx.Exec(ctx, `UPDATE control_delivery SET generation=generation+1,document=jsonb_set(document,'{config}',$1::jsonb),envelope=NULL,issued_at=NULL,expires_at=NULL WHERE singleton`, updated); err != nil {
		return SetupReport{}, adminauth.ErrAuthUnavailable
	}
	if _, err = tx.Exec(ctx, "UPDATE auth_policy SET mode=$1,totp_enabled=$2,version=version+1 WHERE singleton", in.Input.TOTP.Mode, in.Input.TOTP.Enabled); err != nil {
		return SetupReport{}, adminauth.ErrAuthUnavailable
	}
	var at time.Time
	if tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&at) != nil {
		return SetupReport{}, adminauth.ErrAuthUnavailable
	}
	report := SetupReport{review, at, config.Revision, environment, *calibratedAt}
	reportRaw, _ := json.Marshal(report)
	if _, err = tx.Exec(ctx, "UPDATE installation_setup SET password_iterations=$1,report=$2,applied_at=$3 WHERE singleton", c.Iterations, reportRaw, at); err != nil {
		return SetupReport{}, adminauth.ErrAuthUnavailable
	}
	if _, err = tx.Exec(ctx, `INSERT INTO control_audit(actor_id,actor_role,action,target_id,before_digest,after_digest,result,request_id,revision) VALUES('system:setup','system','installation.apply','installation',$1,$2,'applied',$2,$3)`, digest(configRaw), in.PlanDigest, config.Revision); err != nil {
		return SetupReport{}, adminauth.ErrAuthUnavailable
	}
	if tx.Commit(ctx) != nil {
		return SetupReport{}, adminauth.ErrAuthUnavailable
	}
	return report, nil
}
func (s *ControlService) Installation(ctx context.Context, token string) (ControlReply, error) {
	if _, err := s.Config(ctx, token); err != nil {
		return ControlReply{}, err
	}
	report, err := s.store.setupReport(ctx)
	if err != nil {
		return ControlReply{}, err
	}
	raw, _ := json.Marshal(map[string]any{"report": report})
	return ControlReply{Status: 200, Body: raw}, nil
}
