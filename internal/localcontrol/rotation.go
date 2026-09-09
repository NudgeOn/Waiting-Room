// SPDX-License-Identifier: Apache-2.0
package localcontrol

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"os"
	"path/filepath"
	"time"
	"waiting-room/internal/keyring"
)

type rotationDisk struct {
	Binding string      `json:"binding"`
	Keys    keyring.Set `json:"keys"`
	Before  string      `json:"before"`
}
type RotationReport struct {
	Phase          string `json:"phase"`
	Generation     int64  `json:"generation"`
	Digest         string `json:"digest,omitempty"`
	ActivateAt     int64  `json:"activateAt,omitempty"`
	RetireAfter    int64  `json:"retireAfter,omitempty"`
	Acknowledged   int    `json:"acknowledged"`
	EmergencyReady bool   `json:"emergencyReady"`
}

func legacyKeys(s State) keyring.Key {
	cfg := ed25519.NewKeyFromSeed(derive(s, "config"))
	adm := ed25519.NewKeyFromSeed(derive(s, "admission"))
	return keyring.Key{ID: "v1", ConfigPrivate: cfg, ConfigPublic: cfg.Public().(ed25519.PublicKey), AdmissionPrivate: adm, AdmissionPublic: adm.Public().(ed25519.PublicKey), Replay: derive(s, "replay"), Return: derive(s, "return")}
}
func generateKey() (keyring.Key, error) {
	k := keyring.Key{}
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return k, err
	}
	k.ID = hex.EncodeToString(id)
	var err error
	k.ConfigPublic, k.ConfigPrivate, err = ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return k, err
	}
	k.AdmissionPublic, k.AdmissionPrivate, err = ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return k, err
	}
	k.Replay = make([]byte, 32)
	k.Return = make([]byte, 32)
	if _, err = rand.Read(k.Replay); err != nil {
		return k, err
	}
	_, err = rand.Read(k.Return)
	return k, err
}
func loadRotation(root string, s State) (rotationDisk, error) {
	raw, err := ReadPrivate(filepath.Join(root, "rotation.json"), 32768)
	if err != nil {
		return rotationDisk{}, err
	}
	var d rotationDisk
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&d) != nil || dec.Decode(new(any)) != io.EOF || d.Binding != s.Binding() || d.Keys.Validate("owner") != nil || len(d.Before) != 64 {
		return d, keyring.ErrInvalid
	}
	return d, nil
}
func rotationReport(keys keyring.Set, acks int) RotationReport {
	out := RotationReport{Phase: keys.Phase, Generation: keys.Generation, Digest: keys.Digest(), Acknowledged: acks}
	if keys.Previous != nil {
		out.ActivateAt = keys.Current.Since
		out.RetireAfter = keys.Current.Since + keyring.Overlap.Milliseconds()
	}
	return out
}
func countKeyAcks(ctx context.Context, tx pgx.Tx, keys keyring.Set) (int, error) {
	var count int
	err := tx.QueryRow(ctx, `SELECT count(*) FROM control_key_acks a JOIN control_nodes n USING(node_id) JOIN control_delivery d ON d.singleton WHERE a.generation=$1 AND a.digest=$2 AND a.observed_at<=clock_timestamp() AND a.observed_at>clock_timestamp()-interval '2 minutes' AND n.generation=d.generation`, keys.Generation, keys.Digest()).Scan(&count)
	return count, err
}

// RotateKeys is an explicit deployment-owner operation. The caller must stop
// all application roles before mutations (the managed wrctl path enforces it).
// Files are journaled before role writes; retries repair partial distribution.
// No application private key is written to PostgreSQL or the returned report.
func RotateKeys(ctx context.Context, owner *pgxpool.Pool, s State, root, operation string) (RotationReport, error) {
	if operation != "stage" && operation != "activate" && operation != "retire" && operation != "revoke" && operation != "status" {
		return RotationReport{}, keyring.ErrInvalid
	}
	tx, err := owner.Begin(ctx)
	if err != nil {
		return RotationReport{}, err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(72819431061)"); err != nil {
		return RotationReport{}, err
	}
	if _, err = tx.Exec(ctx, "SET LOCAL search_path=waiting_room"); err != nil {
		return RotationReport{}, err
	}
	var binding string
	var now time.Time
	if tx.QueryRow(ctx, "SELECT key_binding,clock_timestamp() FROM install_identity WHERE singleton").Scan(&binding, &now) != nil || binding != s.Binding() {
		return RotationReport{}, keyring.ErrInvalid
	}
	disk, err := loadRotation(root, s)
	if errors.Is(err, os.ErrNotExist) {
		if operation == "status" {
			return RotationReport{Phase: "legacy"}, nil
		}
		if operation != "stage" {
			return RotationReport{}, errors.New("stage deployment keys first")
		}
		for _, role := range []string{"control", "gateway", "coordinator"} {
			n, e := LoadIdentity(filepath.Join(root, role), role)
			if e != nil || n.Keys != nil {
				return RotationReport{}, errors.New("restore the matching key rotation journal")
			}
		}
		disk = rotationDisk{Binding: s.Binding(), Keys: keyring.Set{Current: legacyKeys(s)}, Before: hex.EncodeToString(make([]byte, 32))}
	} else if err != nil {
		return RotationReport{}, err
	}
	before := disk.Keys.Digest()
	changed := false
	acks := 0
	if disk.Keys.Generation > 0 {
		acks, err = countKeyAcks(ctx, tx, disk.Keys)
		if err != nil {
			return RotationReport{}, err
		}
	}
	emergencyReady := false
	if disk.Keys.Phase == "active" && acks == 2 {
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM control_audit WHERE actor_role='admin' AND action='runtime.new_epoch' AND result='accepted' AND occurred_at>=to_timestamp($1::double precision/1000) AND occurred_at>clock_timestamp()-interval '5 minutes' AND occurred_at<=clock_timestamp())`, disk.Keys.Current.Since).Scan(&emergencyReady)
		if err != nil {
			return RotationReport{}, err
		}
	}
	switch operation {
	case "status":
		report := rotationReport(disk.Keys, acks)
		report.EmergencyReady = emergencyReady
		return report, nil
	case "stage":
		if disk.Keys.Phase == "active" {
			return RotationReport{}, errors.New("retire the previous keys before staging another rotation")
		}
		if disk.Keys.Phase != "staged" {
			next, e := generateKey()
			if e != nil {
				return RotationReport{}, e
			}
			disk.Keys.Next = &next
			disk.Keys.Generation++
			disk.Keys.Phase = "staged"
			changed = true
		}
	case "activate":
		if disk.Keys.Phase != "active" {
			if disk.Keys.Phase != "staged" || acks != 2 {
				return RotationReport{}, errors.New("both active data roles must ACK the staged keyring before activation")
			}
			old := disk.Keys.Current
			disk.Keys.Previous = &old
			disk.Keys.Current = *disk.Keys.Next
			disk.Keys.Next = nil
			// Reserve the maximum supported inter-clock margin before the timestamp
			// boundary used for stable promotion signing; all old signers are stopped.
			disk.Keys.Current.Since = now.Add(30 * time.Second).UnixMilli()
			if disk.Keys.Current.Since <= old.Since {
				return RotationReport{}, errors.New("key rotation clock rollback")
			}
			disk.Keys.Generation++
			disk.Keys.Phase = "active"
			changed = true
		}
	case "retire", "revoke":
		if disk.Keys.Phase != "stable" {
			if disk.Keys.Phase != "active" || acks != 2 {
				return RotationReport{}, errors.New("both active data roles must ACK the active keyring before retirement")
			}
			if operation == "revoke" && !emergencyReady {
				return RotationReport{}, errors.New("emergency revocation requires a fresh Admin reauthenticated installation epoch reset and both replica ACKs")
			}
			if operation != "revoke" && now.UnixMilli() < disk.Keys.Current.Since+keyring.Overlap.Milliseconds() {
				return RotationReport{}, errors.New("previous keys still cover live visitor or config artifacts; wait until retireAfter")
			}
			disk.Keys.Previous = nil
			disk.Keys.Generation++
			disk.Keys.Phase = "stable"
			changed = true
		}
	}
	if changed {
		disk.Before = before
	}
	if disk.Keys.Validate("owner") != nil {
		return RotationReport{}, keyring.ErrInvalid
	}
	raw, err := json.Marshal(disk)
	if err != nil {
		return RotationReport{}, err
	}
	if err = writePrivate(root, "rotation.json", raw, true); err != nil {
		return RotationReport{}, err
	}
	for _, role := range []string{"control", "gateway", "coordinator"} {
		dir := filepath.Join(root, role)
		n, e := LoadIdentity(dir, role)
		if e != nil {
			return RotationReport{}, e
		}
		scoped := disk.Keys.ForRole(role)
		n.Keys = &scoped
		k := scoped.Current
		n.ConfigPublic = k.ConfigPublic
		n.AdmissionPublic = k.AdmissionPublic
		n.ConfigPrivate = k.ConfigPrivate
		n.AdmissionPrivate = k.AdmissionPrivate
		n.ReplayKey = k.Replay
		n.ReturnKey = k.Return
		n.SourceKey = nil
		if role == "gateway" {
			n.SourceKey = derive(s, "public-source")
		}
		raw, e = json.Marshal(identityDisk(n))
		if e != nil {
			return RotationReport{}, e
		}
		if e = writePrivate(dir, "identity.json", raw, true); e != nil {
			return RotationReport{}, e
		}
	}
	// Persist one audit event per generation even after a lost commit response.
	tag, err := tx.Exec(ctx, "INSERT INTO control_key_operations(generation,phase,digest,completed_at) VALUES($1,$2,$3,clock_timestamp()) ON CONFLICT(generation) DO NOTHING", disk.Keys.Generation, disk.Keys.Phase, disk.Keys.Digest())
	if err != nil {
		return RotationReport{}, err
	}
	if tag.RowsAffected() == 1 {
		req := sha256.Sum256([]byte("keys/" + disk.Keys.Digest()))
		_, err = tx.Exec(ctx, "INSERT INTO control_audit(actor_id,actor_role,action,target_id,before_digest,after_digest,result,request_id,revision) VALUES('system:wrctl','system',$1,'installation',$2,$3,'applied',$4,$5)", "keys."+operation, disk.Before, disk.Keys.Digest(), hex.EncodeToString(req[:]), disk.Keys.Generation)
		if err != nil {
			return RotationReport{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return RotationReport{}, err
	}
	return rotationReport(disk.Keys, 0), nil
}
