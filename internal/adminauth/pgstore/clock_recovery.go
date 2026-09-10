// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"context"
	"encoding/json"
	"regexp"
	"time"

	"waiting-room/internal/adminauth"
	"waiting-room/internal/configtrust"
	"waiting-room/internal/control"
)

// RecoverClock is available only to an authenticated data role. It re-signs the
// last approved delivery under the same root lock as operator commands. A fresh
// generation already issued by another node/command is returned without a write.
func (s *PublicationService) RecoverClock(ctx context.Context, node string, in configtrust.ClockRecovery) ([]byte, error) {
	if node != "gateway" && node != "coordinator" {
		return nil, adminauth.ErrForbidden
	}
	if in.Generation == 0 || in.Generation >= 9007199254740990 || in.NotBefore <= 0 || in.NotBefore > 253402214399 || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(in.Digest) {
		return nil, control.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	tx, err := authTx(ctx, s.control.store)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	if _, err = readConfig(ctx, tx, true); err != nil {
		return nil, err
	}
	d, generation, err := readDelivery(ctx, tx)
	if err != nil {
		return nil, err
	}
	var now, issued, expires time.Time
	var raw []byte
	if tx.QueryRow(ctx, "SELECT clock_timestamp(),issued_at,expires_at,envelope FROM control_delivery WHERE singleton").Scan(&now, &issued, &expires, &raw) != nil {
		return nil, adminauth.ErrAuthUnavailable
	}
	// A caller clock cannot move the issuer's clock, issue in the future, or
	// resurrect an expired publication. The normal five-minute refresh handles
	// a legitimately expired publisher independently.
	if now.Unix() < in.NotBefore || now.Before(issued) || !now.Before(expires) || uint64(generation) < in.Generation {
		return nil, control.ErrConflict
	}
	if uint64(generation) > in.Generation && issued.Unix() >= in.NotBefore {
		if err = tx.Commit(ctx); err != nil {
			return nil, adminauth.ErrAuthUnavailable
		}
		return raw, nil
	}
	if (uint64(generation) == in.Generation && digest(raw) != in.Digest) || in.NotBefore < issued.Unix() {
		return nil, control.ErrConflict
	}
	before := digest(raw)
	if err = s.sign(ctx, tx, d, generation, now); err != nil {
		return nil, err
	}
	if tx.QueryRow(ctx, "SELECT envelope FROM control_delivery WHERE singleton").Scan(&raw) != nil {
		return nil, adminauth.ErrAuthUnavailable
	}
	request, _ := json.Marshal(in)
	if _, err = tx.Exec(ctx, "INSERT INTO control_audit(actor_id,actor_role,action,target_id,before_digest,after_digest,result,request_id,revision) VALUES($1,'system','config.clock_recovery','installation',$2,$3,'accepted',$4,$5)", "system:"+node, before, digest(raw), digest(request), d.Config.Revision); err != nil {
		return nil, adminauth.ErrAuthUnavailable
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, adminauth.ErrAuthUnavailable
	}
	return raw, nil
}
