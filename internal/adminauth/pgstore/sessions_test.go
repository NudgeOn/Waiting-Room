// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"waiting-room/internal/adminauth"
)

func TestSessionViewProjectionAndExpiryClamp(t *testing.T) {
	now := time.Unix(1700000000, 0)
	snapshot := sessionSnapshot{account: adminauth.Account{ID: "u", Role: adminauth.Viewer},
		session: adminauth.Session{CreatedAt: now.Add(-7*time.Hour - 50*time.Minute), LastSeenAt: now}, csrf: [32]byte{1}}
	view := snapshot.view()
	if view.IdleExpiresAt != view.AbsoluteExpiresAt || view.Role != adminauth.Viewer || view.CapabilityVersion != 1 {
		t.Fatal("incorrect projection")
	}
	for _, capability := range view.Capabilities {
		if capability.Action == adminauth.WriteConfig {
			t.Fatal("viewer write capability")
		}
	}
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"csrf", "token", "password", "sessionVersion"} {
		if strings.Contains(string(raw), s) {
			t.Fatal("sensitive view field")
		}
	}
	if _, err := NewSessionService(nil, "https://admin.test", false); err == nil {
		t.Fatal("nil store accepted")
	}
}
