// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"waiting-room/internal/adminauth"
)

func TestEnrollmentRedactionAndInput(t *testing.T) {
	secret, _ := adminauth.NewSecret()
	key, _ := secret.ProvisioningBase32()
	sensitive := "sensitive-recovery-fixture"
	for _, value := range []any{EnrollmentSetup{secret: secret}, EnrollmentGrant{recovery: [10]string{sensitive}}, recoveryBundle{codes: [10]string{sensitive}}, enrollmentState{sealed: []byte(sensitive)}, EnrollmentService{keyID: sensitive}} {
		b, err := json.Marshal(value)
		rendered := string(b) + fmt.Sprintf("%v %+v %#v", value, value, value)
		if err != nil || strings.Contains(rendered, sensitive) || strings.Contains(rendered, key) {
			t.Fatal("enrollment secret leaked")
		}
	}
	var e EnrollmentService
	if _, err := e.Begin(context.Background(), "invalid"); err != adminauth.ErrInvalidOrReplayed {
		t.Fatal("bad begin token reached DB")
	}
	if _, err := e.Complete(context.Background(), "invalid", "123456"); err != adminauth.ErrInvalidOrReplayed {
		t.Fatal("bad complete token reached DB")
	}
	for _, code := range []string{"", "12345", "1234567", "１２３４５６", "12345x"} {
		if sixDigits(code) {
			t.Fatal("bad OTP shape")
		}
	}
	if !sixDigits("000000") {
		t.Fatal("six digit shape rejected")
	}
	if _, err := NewEnrollmentService(nil, nil, "missing"); err != adminauth.ErrAuthUnavailable {
		t.Fatal("invalid constructor")
	}
	g := EnrollmentGrant{recovery: [10]string{sensitive}}
	copy := g.RecoveryCodes()
	copy[0] = "changed"
	if g.RecoveryCodes()[0] != sensitive {
		t.Fatal("mutable recovery accessor")
	}
}

func TestEnrollmentVaultDomainBinding(t *testing.T) {
	v := testVault(t)
	ref := adminauth.CredentialRef{UserID: "admin1", Version: 1}
	secret, _ := adminauth.NewSecret()
	token := [32]byte{1}
	sealed, err := v.sealEnrollment(ref, "k1", token, secret)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := v.openEnrollment(ref, "k1", token, sealed)
	if err != nil || opened != secret {
		t.Fatal("pending secret roundtrip", err)
	}
	if _, err = v.openEnrollment(ref, "k1", [32]byte{2}, sealed); err == nil {
		t.Fatal("cross-token ciphertext accepted")
	}
	if _, err = v.open(ref, "k1", sealed); err == nil {
		t.Fatal("pending cipher accepted as active")
	}
	active, err := v.Seal(ref, "k1", secret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = v.openEnrollment(ref, "k1", token, active); err == nil {
		t.Fatal("active cipher accepted as pending")
	}
}

type enrollmentCommitDouble struct {
	commitDouble
	late bool
}

func (d *enrollmentCommitDouble) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if d.late && strings.HasPrefix(sql, "UPDATE auth_enrollment_challenges SET consumed=true,key_id=NULL,sealed_secret=NULL WHERE token_hash") {
		d.calls = append(d.calls, sql)
		return pgconn.NewCommandTag("UPDATE 0"), nil
	}
	return d.commitDouble.Exec(ctx, sql, args...)
}
func TestEnrollmentUnknownCommitAndLateExpiry(t *testing.T) {
	for _, late := range []bool{true, false} {
		tx := &enrollmentCommitDouble{commitDouble: commitDouble{commitErr: errors.New("private commit failure")}, late: late}
		ref := adminauth.CredentialRef{UserID: "admin1", Version: 1}
		c := &enrollmentConsumer{tx: tx, attempt: enrollmentAttempt{ref: ref, number: 1}}
		secret, _ := adminauth.ParseSecret("GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ")
		if err := adminauth.VerifyAndConsume(context.Background(), ref, secret, "287082", time.Unix(59, 0), c); err != adminauth.ErrAuthUnavailable {
			t.Fatal("uncertain enrollment accepted", err)
		}
		if late && len(tx.calls) != 1 {
			t.Fatal("credential write after expiry")
		}
		if !late && tx.calls[len(tx.calls)-1] != "COMMIT" {
			t.Fatal("commit not exercised")
		}
	}
}
