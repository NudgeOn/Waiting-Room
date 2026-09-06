// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"encoding/json"
	"testing"

	"waiting-room/internal/control"
)

func TestHTTPErrorMetricsDistinguishUnavailableFromZeroAndValidateSource(t *testing.T) {
	var legacy RoomMetrics
	if err := json.Unmarshal([]byte(`{"roomId":"sale"}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.HTTP5xxLastMinute != nil || legacy.HTTP5xxWindowReady || !validHTTPErrorMetrics("gateway", legacy) {
		t.Fatal("legacy metric became an observed zero")
	}
	zero, positive, negative, tooLarge := int64(0), int64(3), int64(-1), int64(9007199254740990)
	for _, tc := range []struct {
		node  string
		count *int64
		ready bool
		valid bool
	}{
		{"gateway", nil, false, true}, {"coordinator", nil, false, true},
		{"gateway", nil, true, false}, {"coordinator", nil, true, false},
		{"gateway", &zero, false, true}, {"gateway", &zero, true, true},
		{"gateway", &positive, true, true}, {"coordinator", &zero, false, false},
		{"gateway", &negative, true, false}, {"gateway", &tooLarge, true, false},
	} {
		m := RoomMetrics{HTTP5xxLastMinute: tc.count, HTTP5xxWindowReady: tc.ready}
		if validHTTPErrorMetrics(tc.node, m) != tc.valid {
			t.Fatalf("validation mismatch: node=%s metric=%+v", tc.node, m)
		}
	}
	encoded, err := json.Marshal(RoomMetrics{HTTP5xxLastMinute: &zero, HTTP5xxWindowReady: true})
	if err != nil {
		t.Fatal(err)
	}
	var observed RoomMetrics
	if json.Unmarshal(encoded, &observed) != nil || observed.HTTP5xxLastMinute == nil || *observed.HTTP5xxLastMinute != 0 || !observed.HTTP5xxWindowReady {
		t.Fatal("observed zero did not survive delivery JSON")
	}
}

func TestHTTPErrorMetricsACKUsesExactDecoderForBothRolesAndLegacyNodes(t *testing.T) {
	zero := int64(0)
	for _, count := range []*int64{nil, &zero} {
		ack := NodeAck{Generation: 1, Digest: "digest", Rooms: []RoomMetrics{{RoomID: "sale", Revision: 1, Epoch: 1, Mode: "HOLD", HTTP5xxLastMinute: count, HTTP5xxWindowReady: count != nil}}}
		raw, err := json.Marshal(ack)
		if err != nil {
			t.Fatal(err)
		}
		var decoded NodeAck
		if control.DecodeExact(raw, &decoded) != nil {
			t.Fatal("serialized gateway/coordinator ACK rejected by internal endpoint")
		}
		if (decoded.Rooms[0].HTTP5xxLastMinute == nil) != (count == nil) {
			t.Fatal("unavailable observation became an observed zero")
		}
	}
}
