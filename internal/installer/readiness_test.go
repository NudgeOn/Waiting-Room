// SPDX-License-Identifier: Apache-2.0
package installer

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func healthyServices() []serviceStatus {
	var services []serviceStatus
	for _, name := range []string{"postgres", "valkey", "control", "gateway", "demo-origin", "coordinator"} {
		services = append(services, serviceStatus{name, "running", "healthy"})
	}
	return services
}

func TestStartupPreservesReadinessWhileAllowingQueueRecovery(t *testing.T) {
	services := healthyServices()
	services[5].Health = "unhealthy"
	if ready, err := servicesReady(services, 200*time.Second); ready || err != nil {
		t.Fatal("coordinator safety wait rejected or reported ready", ready, err)
	}
	if ready, err := servicesReady(services, coordinatorStartTimeout); ready || err == nil {
		t.Fatal("unbounded recovery wait", ready, err)
	}
	services[5].Health = "healthy"
	if ready, err := servicesReady(services, 200*time.Second); !ready || err != nil {
		t.Fatal(ready, err)
	}
	services[2].Health = "unhealthy"
	if _, err := servicesReady(services, applicationStartTimeout); err == nil {
		t.Fatal("unrelated unhealthy service received recovery grace")
	}
	services = healthyServices()
	services[5].State = "exited"
	if _, err := servicesReady(services, time.Second); err == nil {
		t.Fatal("exited coordinator treated as safety wait")
	}
	if _, err := servicesReady(healthyServices()[:5], applicationStartTimeout); err == nil {
		t.Fatal("missing coordinator treated as recovery")
	}
	services = append(healthyServices(), healthyServices()[0])
	if _, err := servicesReady(services, 0); err == nil {
		t.Fatal("duplicate service accepted")
	}
}

func TestStartupWaitContinuesThroughUnhealthyThenRequiresAllHealthy(t *testing.T) {
	e, f, _, dir := fixture(t)
	healthy, _ := json.Marshal(healthyServices())
	services := healthyServices()
	services[5].Health = "unhealthy"
	holding, _ := json.Marshal(services)
	f.statuses = [][]byte{holding, holding, healthy}
	if err := e.waitReady(context.Background(), dir, installation{}, firstImage, time.Millisecond); err != nil || len(f.calls) != 3 {
		t.Fatal("health transitions not observed", err, len(f.calls))
	}
	f.statuses = [][]byte{holding}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := e.waitReady(ctx, dir, installation{}, firstImage, time.Millisecond); err == nil {
		t.Fatal("cancellation ignored")
	}
	f.statuses = [][]byte{[]byte("malformed")}
	if err := e.waitReady(context.Background(), dir, installation{}, firstImage, time.Millisecond); err == nil {
		t.Fatal("malformed health accepted")
	}
}
