// SPDX-License-Identifier: Apache-2.0
package runtimeplane

import (
	"context"
	"errors"
	"fmt"
	"time"

	"waiting-room/internal/configtrust"
	"waiting-room/internal/control"
	"waiting-room/internal/queue/model"
	"waiting-room/internal/queue/valkeystore"
)

// Only the sync loop owns this state. Never record error messages, URLs,
// credentials, snapshot bodies, ticket IDs or key material in diagnostics.
type syncDiagnostic struct {
	last string
	at   time.Time
}

func (d *syncDiagnostic) observe(role, stage, code string, generation uint64, now time.Time) string {
	if stage == "" {
		if d.last == "" {
			return ""
		}
		d.last = ""
		d.at = now
		return fmt.Sprintf("runtime_sync node=%s state=recovered generation=%d", role, generation)
	}
	key := stage + ":" + code
	if key == d.last && now.Sub(d.at) >= 0 && now.Sub(d.at) < time.Minute {
		return ""
	}
	d.last, d.at = key, now
	return fmt.Sprintf("runtime_sync node=%s state=pending stage=%s code=%s generation=%d", role, stage, code, generation)
}

func syncErrorCode(err error) string {
	for _, c := range []struct {
		err  error
		code string
	}{
		{context.DeadlineExceeded, "deadline"}, {context.Canceled, "cancelled"},
		{configtrust.ErrInvalid, "snapshot_invalid"}, {configtrust.ErrRollback, "snapshot_rollback"},
		{configtrust.ErrPersistence, "snapshot_persistence"}, {configtrust.ErrUnavailable, "snapshot_unavailable"},
		{control.ErrInvalid, "config_invalid"}, {valkeystore.ErrSchema, "queue_schema"},
		{model.ErrConflict, "queue_conflict"}, {model.ErrDrain, "queue_drain"},
		{valkeystore.ErrSweep, "queue_sweep"},
	} {
		if errors.Is(err, c.err) {
			return c.code
		}
	}
	return "unavailable"
}

func syncHTTPCode(status int, err error) string {
	if err != nil {
		return syncErrorCode(err)
	}
	if status >= 100 && status <= 599 && status != 200 {
		return fmt.Sprintf("http_%d", status)
	}
	return "invalid_response"
}
