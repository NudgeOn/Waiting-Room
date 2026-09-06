// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"waiting-room/internal/adminauth"
)

// Failure injection double, not persistence evidence.
type commitDouble struct {
	pgx.Tx
	calls     []string
	commitErr error
	expire    bool
}

func (d *commitDouble) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	d.calls = append(d.calls, sql)
	if d.expire && strings.Contains(sql, "UPDATE auth_totp_challenges") {
		return pgconn.NewCommandTag("UPDATE 0"), nil
	}
	return pgconn.NewCommandTag("UPDATE 1"), nil
}
func (d *commitDouble) Commit(context.Context) error {
	d.calls = append(d.calls, "COMMIT")
	return d.commitErr
}
func TestUnknownCommitAndLateExpiryFailClosed(t *testing.T) {
	for _, expire := range []bool{false, true} {
		tx := &commitDouble{expire: expire, commitErr: errors.New("sensitive unknown commit detail")}
		ref := adminauth.CredentialRef{UserID: "admin1", Version: 1}
		c := &loginConsumer{tx: tx, ref: ref}
		secret, _ := adminauth.ParseSecret("GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ")
		err := adminauth.VerifyAndConsume(context.Background(), ref, secret, "287082", time.Unix(59, 0), c)
		if err != adminauth.ErrAuthUnavailable {
			t.Fatal("uncertain write exposed or accepted")
		}
		if expire && len(tx.calls) != 2 {
			t.Fatal("session created after expiry")
		}
		if !expire && (len(tx.calls) != 4 || tx.calls[3] != "COMMIT") {
			t.Fatal("commit failure not exercised")
		}
	}
}
