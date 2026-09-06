// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
	"waiting-room/internal/adminauth"
	"waiting-room/internal/control"
)

type RoomMetrics struct {
	RoomID              string `json:"roomId"`
	Revision            int64  `json:"revision"`
	Epoch               uint64 `json:"epoch"`
	Mode                string `json:"mode"`
	Waiting             int    `json:"waiting"`
	Ready               int    `json:"ready"`
	Leases              int    `json:"leases"`
	Rate                int    `json:"rate"`
	OriginHealthy       bool   `json:"originHealthy"`
	ArrivalWindowReady  bool   `json:"arrivalWindowReady"`
	ArrivalsFiveMinutes int    `json:"arrivalsFiveMinutes"`
	RecoveryUntil       int64  `json:"recoveryUntil"`
}
type NodeAck struct {
	Generation int64         `json:"generation"`
	Digest     string        `json:"digest"`
	Rooms      []RoomMetrics `json:"rooms"`
}
type NodeView struct {
	ID         string        `json:"id"`
	Generation int64         `json:"generation"`
	ObservedAt time.Time     `json:"observedAt"`
	Fresh      bool          `json:"fresh"`
	Rooms      []RoomMetrics `json:"rooms"`
}
type DeliveryView struct {
	Generation int64                 `json:"generation"`
	Revision   int64                 `json:"revision"`
	State      string                `json:"state"`
	Config     control.Config        `json:"config"`
	Runtimes   []control.RoomRuntime `json:"runtimes"`
	Nodes      []NodeView            `json:"nodes"`
}

func (s *PublicationService) View(ctx context.Context, token string) (ControlReply, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, _, err := s.control.begin(ctx, token, adminauth.ReadDashboard, nil)
	if err != nil {
		return ControlReply{}, err
	}
	defer rollback(tx)
	d, generation, err := readDelivery(ctx, tx)
	if err != nil {
		return ControlReply{}, err
	}
	v, err := deliveryView(ctx, tx, d, generation)
	if err != nil {
		return ControlReply{}, err
	}
	if tx.Commit(ctx) != nil {
		return ControlReply{}, adminauth.ErrAuthUnavailable
	}
	return jsonReply(200, v, configETag(d.Config.Revision)), nil
}
func deliveryView(ctx context.Context, tx pgx.Tx, d control.Delivery, generation int64) (DeliveryView, error) {
	v := DeliveryView{Generation: generation, Revision: d.Config.Revision, State: "pending", Config: d.Config, Runtimes: d.Runtimes, Nodes: []NodeView{}}
	rows, err := tx.Query(ctx, "SELECT node_id,generation,observed_at,observed_at>clock_timestamp()-interval '15 seconds' AND observed_at<=clock_timestamp(),metrics FROM control_nodes ORDER BY node_id")
	if err != nil {
		return v, adminauth.ErrAuthUnavailable
	}
	defer rows.Close()
	for rows.Next() {
		var node NodeView
		var raw []byte
		if rows.Scan(&node.ID, &node.Generation, &node.ObservedAt, &node.Fresh, &raw) != nil || json.Unmarshal(raw, &node.Rooms) != nil {
			return v, adminauth.ErrAuthUnavailable
		}
		v.Nodes = append(v.Nodes, node)
	}
	if rows.Err() != nil {
		return v, adminauth.ErrAuthUnavailable
	}
	if len(v.Nodes) == 2 && generation > 0 {
		v.State = "applied"
		for _, n := range v.Nodes {
			if n.Generation != generation || !n.Fresh {
				v.State = "pending"
			}
		}
	}
	return v, nil
}

// Acknowledge is internal-only. nodeID comes from verified mTLS identity, never
// a JSON/header claim. ACKs must name the exact envelope and complete Room set.
func (s *PublicationService) Acknowledge(ctx context.Context, nodeID string, ack NodeAck) error {
	if nodeID != "gateway" && nodeID != "coordinator" {
		return adminauth.ErrForbidden
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	tx, err := authTx(ctx, s.control.store)
	if err != nil {
		return err
	}
	defer rollback(tx)
	d, generation, err := readDelivery(ctx, tx)
	if err != nil {
		return err
	}
	var envelope []byte
	if tx.QueryRow(ctx, "SELECT envelope FROM control_delivery WHERE singleton AND expires_at>clock_timestamp() AND issued_at<=clock_timestamp()").Scan(&envelope) != nil {
		return adminauth.ErrAuthUnavailable
	}
	if generation != ack.Generation || digest(envelope) != ack.Digest || len(ack.Rooms) != len(d.Config.Rooms) {
		return control.ErrConflict
	}
	for i, m := range ack.Rooms {
		room, runtime, ok := d.Find(m.RoomID)
		if !ok || m.RoomID != d.Config.Rooms[i].ID || m.Revision != runtime.Revision || m.Epoch != runtime.Epoch || m.Waiting < 0 || m.Ready < 0 || m.Leases < 0 || m.Rate < 0 || m.Waiting > 100000 || m.Ready > 100000 || m.Leases > 100000 || m.Rate > 60000 || m.ArrivalsFiveMinutes < 0 || m.ArrivalsFiveMinutes > 100000000 || m.RecoveryUntil < 0 {
			return control.ErrInvalid
		}
		if m.Mode != runtime.Mode && m.Mode != "RECOVERY_HOLD" {
			return control.ErrInvalid
		}
		if !room.Active && m.Mode != "OFF" {
			return control.ErrInvalid
		}
	}
	raw, _ := json.Marshal(ack.Rooms)
	if _, err = tx.Exec(ctx, "INSERT INTO control_nodes(node_id,generation,envelope_digest,observed_at,metrics) VALUES($1,$2,$3,clock_timestamp(),$4) ON CONFLICT(node_id) DO UPDATE SET generation=EXCLUDED.generation,envelope_digest=EXCLUDED.envelope_digest,observed_at=EXCLUDED.observed_at,metrics=EXCLUDED.metrics WHERE control_nodes.generation<=EXCLUDED.generation", nodeID, generation, ack.Digest, raw); err != nil {
		return adminauth.ErrAuthUnavailable
	}
	return tx.Commit(ctx)
}
