// SPDX-License-Identifier: Apache-2.0
package lab

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"unicode/utf8"
)

func FuzzJoinTarget(f *testing.F) {
	for _, raw := range []string{`{"target":"/shop"}`, ` {"target":"/shop/item?x=1"} `, `{"target":"//example.invalid"}`, `{"target":"/shop","target":"/shop/item"}`, `{"Target":"/shop"}`, `null`, "{\"target\":\"/shop/\xff\"}"} {
		f.Add([]byte(raw))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		r := httptest.NewRequest("POST", "/_wr/v1/tickets", bytes.NewReader(raw))
		target, err := joinTarget(httptest.NewRecorder(), r)
		if err != nil {
			return
		}
		if len(raw) > 4096 || !utf8.Valid(raw) || !ValidTarget(target) {
			t.Fatal("accepted input violates bound/encoding/target")
		}
		var obj map[string]json.RawMessage
		var expected string
		if json.Unmarshal(raw, &obj) != nil || len(obj) != 1 || json.Unmarshal(obj["target"], &expected) != nil || expected != target {
			t.Fatal("accepted input disagrees with exact schema")
		}
		encoded, _ := json.Marshal(map[string]string{"target": target})
		again, err := joinTarget(httptest.NewRecorder(), httptest.NewRequest("POST", "/_wr/v1/tickets", bytes.NewReader(encoded)))
		// JSON HTML escaping can enlarge a valid target beyond the independent
		// wire-body budget. That encoding must be rejected, not called unstable.
		if len(encoded) > 4096 {
			if err == nil {
				t.Fatal("expanded encoding exceeded wire budget")
			}
			return
		}
		if err != nil || again != target {
			t.Fatal("normalization not stable")
		}
	})
}
