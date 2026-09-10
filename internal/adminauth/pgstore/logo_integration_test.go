//go:build integration

// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/jpeg"
	"strings"
	"testing"

	"waiting-room/internal/control"
)

func TestControlLogoDraftSanitizeReplayLifecycle(t *testing.T) {
	f, s, g := controlFixture(t)
	ctx := context.Background()
	var upload bytes.Buffer
	if err := jpeg.Encode(&upload, image.NewRGBA(image.Rect(0, 0, 32, 16)), nil); err != nil {
		t.Fatal(err)
	}
	upload.WriteString("private-upload-metadata")
	c := draftDocument()
	c.Rooms[0].Theme.LogoImage = base64.StdEncoding.EncodeToString(upload.Bytes())
	want, err := control.SanitizeLogo(c.Rooms[0].Theme.LogoImage)
	if err != nil {
		t.Fatal(err)
	}
	r := draftRequest(g, "logo-upload-replay", `"config-0"`)
	one, err := s.ReplaceDraft(ctx, g.SessionToken(), r, c.Bytes())
	if err != nil || one.Status != 200 {
		t.Fatal(one, err)
	}
	var saved control.Config
	if control.DecodeExact(one.Body, &saved) != nil || saved.Validate() != nil || saved.Rooms[0].Theme.LogoImage != want || saved.Rooms[0].Theme.LogoImage == c.Rooms[0].Theme.LogoImage {
		t.Fatal("not sanitized before persistence")
	}
	other, _ := NewControlService(f.replica, "https://admin.test")
	two, err := other.ReplaceDraft(ctx, g.SessionToken(), r, c.Bytes())
	if err != nil || !two.Replay || !bytes.Equal(one.Body, two.Body) {
		t.Fatal("upload exact retry changed", err)
	}
	read, err := other.Config(ctx, g.SessionToken())
	if err != nil || !bytes.Equal(read.Body, one.Body) {
		t.Fatal("replica did not preserve canonical image", err)
	}
	saved.Rooms[0].Theme.LogoImage = ""
	removed, err := other.ReplaceDraft(ctx, g.SessionToken(), draftRequest(g, "logo-remove-replay", one.ETag), saved.Bytes())
	if err != nil || removed.Status != 200 || bytes.Contains(removed.Body, []byte(`"logoImage"`)) {
		t.Fatal("removal not persisted", err)
	}
	audit, err := s.Audit(ctx, g.SessionToken(), 0)
	if err != nil || len(audit) != 2 {
		t.Fatal("retry duplicated audit", err)
	}
}

func TestControlLogoRejectionIsDurableWithoutDraftChange(t *testing.T) {
	_, s, g := controlFixture(t)
	ctx := context.Background()
	c := draftDocument()
	c.Rooms[0].Theme.LogoImage = base64.StdEncoding.EncodeToString([]byte("<svg onload='alert(1)'/>"))
	r := draftRequest(g, "unsafe-logo-retry", `"config-0"`)
	one, err := s.ReplaceDraft(ctx, g.SessionToken(), r, c.Bytes())
	if err != nil || one.Status != 422 || !bytes.Contains(one.Body, []byte("INVALID_THEME_IMAGE")) {
		t.Fatal(one, err)
	}
	two, err := s.ReplaceDraft(ctx, g.SessionToken(), r, c.Bytes())
	if err != nil || !two.Replay || !bytes.Equal(one.Body, two.Body) {
		t.Fatal("rejection not stable", err)
	}
	read, err := s.Config(ctx, g.SessionToken())
	if err != nil || read.ETag != `"config-0"` {
		t.Fatal("rejected image changed draft", err)
	}
	audit, err := s.Audit(ctx, g.SessionToken(), 0)
	if err != nil || len(audit) != 1 || audit[0].Result != "rejected" {
		t.Fatal("rejection audit", err)
	}
}

func TestControlDraftReservesSignedDeliveryCapacity(t *testing.T) {
	_, s, g := controlFixture(t)
	c := draftDocument()
	c.Rooms = nil
	for i := 0; i < 100; i++ {
		r := draftRoom()
		r.ID = fmt.Sprintf("r%d", i)
		r.PublicID = strings.Repeat("a", 18) + string(rune('a'+i/26)) + string(rune('a'+i%26))
		r.Theme.Message = strings.Repeat("a", 400)
		c.Rooms = append(c.Rooms, r)
		if !c.FitsDeliveryBudget() {
			break
		}
	}
	if c.FitsDeliveryBudget() || c.Validate() != nil || len(c.Bytes()) > control.MaxConfigBytes {
		t.Fatal("capacity fixture invalid")
	}
	r := draftRequest(g, "logo-capacity-retry", `"config-0"`)
	one, err := s.ReplaceDraft(context.Background(), g.SessionToken(), r, c.Bytes())
	if err != nil || one.Status != 422 || !bytes.Contains(one.Body, []byte("CONFIG_CAPACITY_EXCEEDED")) {
		t.Fatal(one, err)
	}
	two, err := s.ReplaceDraft(context.Background(), g.SessionToken(), r, c.Bytes())
	if err != nil || !two.Replay || !bytes.Equal(one.Body, two.Body) {
		t.Fatal("capacity rejection replay", err)
	}
}
