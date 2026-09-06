// SPDX-License-Identifier: Apache-2.0
package valkeystore

import (
	"context"
	"testing"
	"waiting-room/internal/queue/model"
)

func TestInstallationConfig(t *testing.T) {
	for _, tt := range []struct {
		name  string
		c     InstallationConfig
		room  model.Config
		valid bool
	}{
		{"standard", StandardInstallation(), model.DefaultConfig(), true},
		{"high", InstallationConfig{"high", 100000, 200000}, model.DefaultConfig(), true},
		{"unknown", InstallationConfig{"other", 10000, 20000}, model.DefaultConfig(), false},
		{"too_many_visitors", InstallationConfig{"standard", 10001, 20000}, model.DefaultConfig(), false},
		{"too_many_idems", InstallationConfig{"standard", 10000, 20001}, model.DefaultConfig(), false},
		{"room_exceeds_installation", InstallationConfig{"standard", 9999, 20000}, model.DefaultConfig(), false},
		{"empty", InstallationConfig{}, model.DefaultConfig(), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if (tt.c.validate(tt.room) == nil) != tt.valid {
				t.Fatal("unexpected validation")
			}
		})
	}
	c := model.DefaultConfig()
	c.Rate = 6001
	if StandardInstallation().validate(c) == nil {
		t.Fatal("profile rate exceeded")
	}
	c = model.DefaultConfig()
	c.LeaseCap = 10001
	if StandardInstallation().validate(c) == nil {
		t.Fatal("invalid room accepted")
	}
}

func TestOpenRoomRejectsUnsafeKeysBeforeDial(t *testing.T) {
	for _, tt := range []struct{ namespace, room string }{{"production", "room"}, {"wr:lab:safe", "bad:room"}, {"wr:lab:safe", "installation"}, {"wr:lab:safe", ""}} {
		if _, err := OpenRoom(context.Background(), "invalid-address", tt.namespace, tt.room, model.DefaultConfig(), StandardInstallation()); err != ErrSchema {
			t.Fatalf("%+v: %v", tt, err)
		}
	}
}
