// SPDX-License-Identifier: Apache-2.0
package installer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
)

const applicationStartTimeout = 180 * time.Second

// A persisted queue may hold admissions for the maximum supported lease (3600s)
// plus 30s safety margin after primary restart. Allow startup overhead as well.
// This changes only how long the installer waits, never the readiness gate.
const coordinatorStartTimeout = 3700 * time.Second

func decodeServices(b []byte) ([]serviceStatus, error) {
	var raw []serviceStatus
	input := strings.TrimSpace(string(b))
	if strings.HasPrefix(input, "[") {
		if json.Unmarshal(b, &raw) != nil {
			return nil, errors.New("runtime status format unavailable")
		}
	} else if input != "" {
		d := json.NewDecoder(strings.NewReader(input))
		for {
			var item serviceStatus
			err := d.Decode(&item)
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, errors.New("runtime status format unavailable")
			}
			raw = append(raw, item)
		}
	}
	return raw, nil
}

func servicesReady(services []serviceStatus, elapsed time.Duration) (bool, error) {
	required := map[string]time.Duration{"postgres": applicationStartTimeout, "valkey": applicationStartTimeout, "control": applicationStartTimeout, "gateway": applicationStartTimeout, "demo-origin": applicationStartTimeout, "coordinator": coordinatorStartTimeout}
	seen := map[string]bool{}
	ready := true
	for _, service := range services {
		limit, ok := required[service.Service]
		if !ok {
			continue // Completed initialization jobs are not runtime services.
		}
		if seen[service.Service] {
			return false, errors.New("duplicate runtime service")
		}
		seen[service.Service] = true
		if service.State != "running" || service.Health != "healthy" {
			ready = false
			if service.State == "exited" || service.State == "dead" || elapsed >= limit {
				return false, errors.New("runtime service did not become ready")
			}
		}
	}
	for name := range required {
		if !seen[name] {
			ready = false
			if elapsed >= applicationStartTimeout {
				return false, errors.New("required runtime service is missing")
			}
		}
	}
	return ready, nil
}

func (e engine) waitReady(ctx context.Context, dir string, s installation, image string, interval time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, coordinatorStartTimeout)
	defer cancel()
	started := time.Now()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		b, err := e.compose(ctx, dir, s, image, "ps", "--all", "--format", "json")
		if err != nil {
			return err
		}
		services, err := decodeServices(b)
		if err != nil {
			return err
		}
		ready, err := servicesReady(services, time.Since(started))
		if err != nil || ready {
			return err
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
