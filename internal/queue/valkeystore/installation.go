// SPDX-License-Identifier: Apache-2.0
package valkeystore

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"

	valkey "github.com/valkey-io/valkey-go"
	"waiting-room/internal/queue/model"
)

// InstallationConfig permits smaller lab quotas, never a larger profile ceiling.
type InstallationConfig struct {
	Profile                    string
	VisitorCap, IdempotencyCap int
}

type Capacity struct {
	Visitors, Idempotency, VisitorCap, IdempotencyCap int
	RetainedVisitors, RetainedIdempotency             int
	Warning                                           bool
}

func StandardInstallation() InstallationConfig {
	return InstallationConfig{Profile: "standard", VisitorCap: 10000, IdempotencyCap: 20000}
}

func (c InstallationConfig) validate(room model.Config) error {
	visitors, idems, rate := 10000, 20000, 6000
	switch c.Profile {
	case "standard":
	case "high":
		visitors, idems, rate = 100000, 200000, 60000
	default:
		return ErrSchema
	}
	if c.VisitorCap < 1 || c.VisitorCap > visitors || c.IdempotencyCap < 1 || c.IdempotencyCap > idems || room.VisitorCap > c.VisitorCap || room.IdempotencyCap > c.IdempotencyCap || room.Rate > rate {
		return ErrSchema
	}
	_, err := model.New(room)
	return err
}

// OpenRoom is an unsharded, single-primary lab path, not HA qualification.
// A fresh namespace explicitly creates an installation; existing configuration is immutable.
func OpenRoom(ctx context.Context, address, namespace, room string, config model.Config, installation InstallationConfig) (*Store, error) {
	if !regexp.MustCompile(`^wr:lab:[a-zA-Z0-9_-]{1,80}$`).MatchString(namespace) || !regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`).MatchString(room) || room == "installation" {
		return nil, ErrSchema
	}
	if err := installation.validate(config); err != nil {
		return nil, err
	}
	c, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{address}, ForceSingleClient: true, DisableCache: true, DisableRetry: true})
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			c.Close()
		}
	}()
	cluster, err := c.Do(ctx, c.B().Info().Section("cluster").Build()).ToString()
	if err != nil {
		return nil, err
	}
	if !strings.Contains(cluster, "cluster_enabled:0\r\n") {
		return nil, ErrSchema
	}
	s := &Store{client: c, installation: true}
	for _, suffix := range []string{"meta", "tickets", "waiting", "expiry", "leases", "rate", "idempotency", "idem-expiry"} {
		s.keys = append(s.keys, namespace+"{"+room+":1}:"+suffix)
	}
	for _, suffix := range []string{"meta", "visitors", "idempotency"} {
		s.keys = append(s.keys, namespace+"{installation:1}:"+suffix)
	}
	s.primary, err = s.primaryID(ctx)
	if err != nil {
		return nil, err
	}
	if err = s.load(ctx); err != nil {
		return nil, err
	}
	data, _ := json.Marshal(config)
	global, _ := json.Marshal(installation)
	if err = c.Do(ctx, c.B().Fcall().Function("wr_i2_init").Numkeys(11).Key(s.keys...).Arg(string(data), s.primary, string(global), room).Build()).Error(); err != nil {
		return nil, classify(err)
	}
	success = true
	return s, nil
}

func (s *Store) Capacity(ctx context.Context) (Result, error) {
	if !s.installation {
		return Result{}, ErrSchema
	}
	return s.call(ctx, true)
}
