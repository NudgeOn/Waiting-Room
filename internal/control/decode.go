// SPDX-License-Identifier: Apache-2.0
package control

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"
)

const MaxConfigBytes = 65536

// DecodeExact accepts all required fields exactly once (omitempty is optional); no case folding, null,
// unknown fields, duplicate keys, or trailing document. Types remain typed JSON.
func DecodeExact(raw []byte, out any) error {
	if len(raw) == 0 || len(raw) > MaxConfigBytes || !utf8.Valid(raw) || out == nil {
		return ErrInvalid
	}
	t := reflect.TypeOf(out)
	if t.Kind() != reflect.Pointer || reflect.ValueOf(out).IsNil() {
		return ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	if !shape(d, t.Elem(), 0) {
		return ErrInvalid
	}
	if _, e := d.Token(); e != io.EOF {
		return ErrInvalid
	}
	if json.Unmarshal(raw, out) != nil {
		return ErrInvalid
	}
	return nil
}
func shape(d *json.Decoder, t reflect.Type, depth int) bool {
	if depth > 16 {
		return false
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	v, e := d.Token()
	if e != nil || v == nil {
		return false
	}
	if t == reflect.TypeOf(time.Time{}) {
		text, ok := v.(string)
		if !ok {
			return false
		}
		_, err := time.Parse(time.RFC3339Nano, text)
		return err == nil
	}
	switch t.Kind() {
	case reflect.Struct:
		if v != json.Delim('{') {
			return false
		}
		fields := map[string]reflect.Type{}
		required := map[string]bool{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			key := strings.Split(f.Tag.Get("json"), ",")[0]
			if key == "" || key == "-" {
				return false
			}
			fields[key] = f.Type
			required[key] = !strings.Contains(","+f.Tag.Get("json")+",", ",omitempty,")
		}
		seen := map[string]bool{}
		for d.More() {
			k, e := d.Token()
			key, ok := k.(string)
			ft, exists := fields[key]
			if e != nil || !ok || !exists || seen[key] || !shape(d, ft, depth+1) {
				return false
			}
			seen[key] = true
		}
		end, e := d.Token()
		if e != nil || end != json.Delim('}') {
			return false
		}
		for key, must := range required {
			if must && !seen[key] {
				return false
			}
		}
		return true
	case reflect.Slice:
		if v != json.Delim('[') {
			return false
		}
		for d.More() {
			if !shape(d, t.Elem(), depth+1) {
				return false
			}
		}
		end, e := d.Token()
		return e == nil && end == json.Delim(']')
	case reflect.String:
		_, ok := v.(string)
		return ok
	case reflect.Bool:
		_, ok := v.(bool)
		return ok
	case reflect.Int, reflect.Int64, reflect.Uint64:
		_, ok := v.(float64)
		return ok
	}
	return false
}
