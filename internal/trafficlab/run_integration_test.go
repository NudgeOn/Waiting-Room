//go:build integration

// SPDX-License-Identifier: Apache-2.0
package trafficlab

import (
	"context"
	"encoding/json"
	valkey "github.com/valkey-io/valkey-go"
	"os"
	"strings"
	"testing"
	"waiting-room/internal/queue/valkeystore"
)

func TestTrafficLabRealHTTPPresets(t *testing.T) {
	if os.Getenv("WR_TEST_TRAFFIC_VALKEY") != "127.0.0.1:16379" {
		t.Fatal("dedicated WR_TEST_TRAFFIC_VALKEY=127.0.0.1:16379 required")
	}
	options := valkey.ClientOption{InitAddress: []string{"127.0.0.1:16379"}, DisableCache: true, ForceSingleClient: true}
	client, err := valkey.NewClient(options)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx := context.Background()
	if err = valkeystore.InstallRuntimeLibrary(ctx, client); err != nil {
		t.Fatal(err)
	}
	execute := NewExecutor(options)
	for _, preset := range []string{"quick-20", "smoke-1k"} {
		t.Run(preset, func(t *testing.T) {
			last := 0
			report, err := execute(ctx, Input{strings.Repeat("A", 43), preset}, func(r Report) error {
				if !r.Valid(preset) || r.Joined < last {
					t.Error("invalid progress")
				}
				last = r.Joined
				return nil
			})
			if err != nil || report.State != "passed" || !report.Valid(preset) {
				raw, _ := json.Marshal(report)
				t.Fatalf("run failed: %v %s", err, raw)
			}
			if report.Admitted != 3 || report.Queued != Visitors(preset)-3 || report.ExpectedRejections != 3 || len(report.Timeline) != 20 {
				t.Fatal("incorrect evidence", report)
			}
			if n, e := client.Do(ctx, client.B().Exists().Key(fixtureKeys()...).Build()).ToInt64(); e != nil || n != 0 {
				t.Fatal("fixture keys retained", n, e)
			}
			raw, _ := json.Marshal(report)
			for _, secret := range []string{"ticketToken", "admissionToken", "Cookie", "Bearer", "http://"} {
				if strings.Contains(string(raw), secret) {
					t.Fatal("credential or private URL in report")
				}
			}
			t.Logf("visitors=%d admitted=%d queued=%d requests=%d durationMs=%d", report.Visitors, report.Admitted, report.Queued, report.Requests, report.DurationMS)
		})
	}
}
