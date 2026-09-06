// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"waiting-room/internal/adminauth"
)

func TestRecoveryRedactionAndInvalidInputs(t *testing.T) {
	secret := "sensitive-recovery-hash"
	value := recoveryAttempt{records: [10]recoveryRecord{{encoded: secret}}}
	b, err := json.Marshal(value)
	if err != nil || strings.Contains(string(b)+fmt.Sprintf("%v %+v %#v", value, value, value), secret) {
		t.Fatal("recovery snapshot exposed")
	}
	if _, err = NewRecoveryService(nil, nil); err != adminauth.ErrAuthUnavailable {
		t.Fatal("nil dependencies accepted")
	}
	r := &RecoveryService{}
	if _, err = r.Complete(context.Background(), "bad-token", "code"); err != adminauth.ErrInvalidOrReplayed {
		t.Fatal("malformed token reached DB")
	}
	for _, slot := range []int{-1, 0, 11} {
		if _, err = r.finish(context.Background(), value, slot); err != adminauth.ErrInvalidOrReplayed {
			t.Fatal("bad slot reached DB")
		}
	}
	if _, err = r.match(context.Background(), value, "bad-code"); err != adminauth.ErrInvalidOrReplayed {
		t.Fatal("malformed code reached KDF")
	}
}

type recoveryCommitDouble struct {
	commitDouble
	reject string
}

func (d *recoveryCommitDouble) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if d.reject != "" && strings.HasPrefix(sql, d.reject) {
		d.calls = append(d.calls, sql)
		return pgconn.NewCommandTag("UPDATE 0"), nil
	}
	return d.commitDouble.Exec(ctx, sql, args...)
}
func TestRecoveryUnknownCommitAndGuardFailure(t *testing.T) {
	for _, prefix := range []string{"UPDATE auth_recovery_codes", "UPDATE auth_totp_challenges", ""} {
		tx := &recoveryCommitDouble{commitDouble: commitDouble{commitErr: errors.New("private commit response lost")}, reject: prefix}
		if err := commitRecovery(context.Background(), tx, recoveryAttempt{number: 1}, 1, [32]byte{}, [32]byte{}, nil); err == nil {
			t.Fatal("uncertain write accepted")
		}
		expected := 4
		if prefix == "UPDATE auth_recovery_codes" {
			expected = 1
		}
		if prefix == "UPDATE auth_totp_challenges" {
			expected = 2
		}
		if len(tx.calls) != expected {
			t.Fatal("write continued after guard failure")
		}
		if prefix == "" && tx.calls[3] != "COMMIT" {
			t.Fatal("commit not exercised")
		}
	}
}
