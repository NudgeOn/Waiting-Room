// SPDX-License-Identifier: Apache-2.0
package lab

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"unicode/utf8"
)

var errJoinInput = errors.New("invalid join input")

func jsonContentType(h http.Header) bool {
	if len(h.Values("Content-Type")) != 1 {
		return false
	}
	media, params, err := mime.ParseMediaType(h.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return false
	}
	for key, value := range params {
		if key != "charset" || !strings.EqualFold(value, "utf-8") {
			return false
		}
	}
	return true
}

// Token-level field parsing rejects duplicate and case-variant target fields.
// Whitespace and valid JSON string escapes remain supported; the body is bounded
// before decoding and never persisted or echoed in an error.
func joinTarget(w http.ResponseWriter, r *http.Request) (string, error) {
	return joinTargetFor(w, r, ValidTarget)
}
func joinTargetFor(w http.ResponseWriter, r *http.Request, valid func(string) bool) (string, error) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
	if err != nil || !utf8.Valid(raw) {
		return "", errJoinInput
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	first, err := d.Token()
	if err != nil || first != json.Delim('{') {
		return "", errJoinInput
	}
	seen := false
	target := ""
	for d.More() {
		field, err := d.Token()
		if err != nil || field != "target" || seen {
			return "", errJoinInput
		}
		seen = true
		if d.Decode(&target) != nil {
			return "", errJoinInput
		}
	}
	end, err := d.Token()
	if err != nil || end != json.Delim('}') || !seen || d.Decode(new(any)) != io.EOF || !valid(target) {
		return "", errJoinInput
	}
	return target, nil
}
