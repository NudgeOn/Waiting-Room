//go:build integration

// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"context"
	"errors"
	"net/netip"
	"sync"
	"testing"
	"waiting-room/internal/adminauth"
	"waiting-room/internal/installplan"
)

func setupWizardFixture(t *testing.T) (fixture, *SetupService, string, installplan.Input) {
	f := setup(t)
	for _, sql := range []string{Migration005, Migration006, Migration010, Migration011} {
		execSQL(t, f.pool, sql)
	}
	if err := f.store.InitializeControl(context.Background(), "standard-10k", "local"); err != nil {
		t.Fatal(err)
	}
	f.store.authAudit = true
	s := NewSetupService(f.store)
	s.measure = func(context.Context) (adminauth.Calibration, error) {
		return adminauth.Calibration{Version: 1, MemoryKiB: 65536, Parallelism: 1, Iterations: 3, TargetMet: true, Samples: []adminauth.CalibrationSample{{Iterations: 3, MedianMS: 300}}}, nil
	}
	token, err := f.store.IssueBootstrapToken(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := s.Inspect(context.Background(), token.Token())
	if err != nil {
		t.Fatal(err)
	}
	in := inspection.Input
	in.RegionID = "seoul"
	in.TOTP.Enabled = false
	in.Limits.AdmissionsPerMinute = 23
	return f, s, token.Token(), in
}
func reviewedSetup(t *testing.T, s *SetupService, token string, in installplan.Input) installplan.SetupApply {
	t.Helper()
	ctx := context.Background()
	if _, err := s.Calibrate(ctx, token); err != nil {
		t.Fatal(err)
	}
	r, err := s.Plan(ctx, token, in)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.Plan(ctx, token, in)
	if err != nil || r.PlanDigest != again.PlanDigest {
		t.Fatal("unstable dry-run")
	}
	return installplan.SetupApply{Input: in, PlanDigest: r.PlanDigest, CalibrationDigest: r.CalibrationDigest}
}
func TestSetupApplyAtomicConcurrentAndBootstrap(t *testing.T) {
	f, s, token, in := setupWizardFixture(t)
	ctx := context.Background()
	passwords := adminauth.NewPasswordHasherSource(f.store.PasswordIterations)
	login, err := NewLoginService(f.store, passwords, [32]byte{1}, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = login.Bootstrap(ctx, token, "wizard_admin", "local setup password fixture", netip.MustParseAddr("127.0.0.1")); !errors.Is(err, ErrSetupReview) {
		t.Fatal("bootstrap bypassed setup", err)
	}
	input := reviewedSetup(t, s, token, in)
	var wg sync.WaitGroup
	results := make(chan error, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := s.Apply(ctx, token, input); results <- e }()
	}
	wg.Wait()
	close(results)
	for e := range results {
		if e != nil && !errors.Is(e, ErrSetupReview) {
			t.Fatal("concurrent apply", e)
		}
	}
	// Policy changed by the winner: resnapshot retries must return the same report.
	first, err := s.Apply(ctx, token, input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewSetupService(f.replica).Apply(ctx, token, input)
	if err != nil || !first.AppliedAt.Equal(second.AppliedAt) {
		t.Fatal("restart/retry changed result", err)
	}
	var count int
	f.pool.QueryRow(ctx, "SELECT count(*) FROM control_audit WHERE action='installation.apply'").Scan(&count)
	if count != 1 {
		t.Fatal("duplicate audit", count)
	}
	if n, err := f.store.PasswordIterations(ctx); err != nil || n != 3 {
		t.Fatal("parameters not applied")
	}
	input.Input.RegionID = "changed"
	if _, err = s.Apply(ctx, token, input); !errors.Is(err, ErrSetupConflict) {
		t.Fatal("reapplied different setup", err)
	}
	result, err := login.Bootstrap(ctx, token, "wizard_admin", "local setup password fixture", netip.MustParseAddr("127.0.0.1"))
	if err != nil || result.SessionGrant().SessionToken() == "" {
		t.Fatal("bootstrap after setup", err)
	}
	if _, err = s.Inspect(ctx, token); !errors.Is(err, adminauth.ErrUnauthenticated) {
		t.Fatal("completed setup token remained usable", err)
	}
	if _, err = f.store.IssueBootstrapToken(ctx); !errors.Is(err, adminauth.ErrUnauthenticated) {
		t.Fatal("bootstrap tombstone lost")
	}
	restarted := adminauth.NewPasswordHasherSource(f.replica.PasswordIterations)
	l, _ := NewLoginService(f.replica, restarted, [32]byte{1}, true)
	if _, err = l.Login(ctx, "wizard_admin", "local setup password fixture", netip.MustParseAddr("127.0.0.1")); err != nil {
		t.Fatal("login after restart", err)
	}
}
func TestSetupRejectsStaleCalibrationAndRollsBackAuditFailure(t *testing.T) {
	f, s, token, in := setupWizardFixture(t)
	ctx := context.Background()
	input := reviewedSetup(t, s, token, in)
	changed := input
	changed.Input.Limits.AdmissionsPerMinute++
	if _, err := s.Apply(ctx, token, changed); !errors.Is(err, ErrSetupReview) {
		t.Fatal("modified plan accepted", err)
	}
	execSQL(t, f.pool, "CREATE FUNCTION fail_setup_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture'; END $$; CREATE TRIGGER fail_setup_audit BEFORE INSERT ON control_audit FOR EACH ROW EXECUTE FUNCTION fail_setup_audit()")
	if _, err := s.Apply(ctx, token, input); !errors.Is(err, adminauth.ErrAuthUnavailable) {
		t.Fatal("audit failure accepted", err)
	}
	var revision int
	f.pool.QueryRow(ctx, "SELECT revision FROM control_config").Scan(&revision)
	if revision != 0 {
		t.Fatal("config not rolled back")
	}
	if n, _ := f.store.PasswordIterations(ctx); n != 2 {
		t.Fatal("password parameters not rolled back")
	}
	execSQL(t, f.pool, "DROP TRIGGER fail_setup_audit ON control_audit; UPDATE installation_setup SET calibrated_at=clock_timestamp()-interval '16 minutes'")
	if _, err := s.Apply(ctx, token, input); !errors.Is(err, ErrSetupReview) {
		t.Fatal("expired measurement accepted", err)
	}
	rotated, err := f.store.IssueBootstrapToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Calibrate(ctx, token); !errors.Is(err, adminauth.ErrUnauthenticated) {
		t.Fatal("rotated token accepted")
	}
	if _, err = s.Plan(ctx, rotated.Token(), in); !errors.Is(err, ErrSetupReview) {
		t.Fatal("old generation measurement accepted")
	}
}

func TestSetupMigrationPreservesExistingCredentialsAndPolicy(t *testing.T) {
	f, oldLogin, token := bootstrapFixture(t)
	ctx := context.Background()
	execSQL(t, f.pool, "UPDATE auth_policy SET totp_enabled=false")
	if _, err := oldLogin.Bootstrap(ctx, token.Token(), "legacy_admin", testPassword, testPeer); err != nil {
		t.Fatal(err)
	}
	var before, after string
	if err := f.pool.QueryRow(ctx, "SELECT password_hash FROM auth_password_credentials WHERE user_id='legacy_admin'").Scan(&before); err != nil {
		t.Fatal(err)
	}
	execSQL(t, f.pool, Migration011)
	passwords := adminauth.NewPasswordHasherSource(f.replica.PasswordIterations)
	newLogin, err := NewLoginService(f.replica, passwords, [32]byte{1}, true)
	if err != nil {
		t.Fatal(err)
	}
	result, err := newLogin.Login(ctx, "legacy_admin", testPassword, testPeer)
	if err != nil || result.SessionGrant().SessionToken() == "" {
		t.Fatal("legacy login or explicit OFF policy changed", err)
	}
	if err := f.pool.QueryRow(ctx, "SELECT password_hash FROM auth_password_credentials WHERE user_id='legacy_admin'").Scan(&after); err != nil || before != after {
		t.Fatal("migration or login rewrote existing credentials", err)
	}
	if n, err := f.replica.PasswordIterations(ctx); err != nil || n != 2 {
		t.Fatal("legacy parameters changed", err)
	}
	if report, err := f.replica.setupReport(ctx); err != nil || report != nil {
		t.Fatal("migration invented an applied report", err)
	}
	if _, err := NewSetupService(f.replica).Inspect(ctx, token.Token()); !errors.Is(err, adminauth.ErrUnauthenticated) {
		t.Fatal("migration reopened first-admin setup", err)
	}
}
