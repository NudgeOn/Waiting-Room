// SPDX-License-Identifier: Apache-2.0
package valkeystore

import (
	"context"
	_ "embed"
	"strings"

	valkey "github.com/valkey-io/valkey-go"
)

//go:embed maintenance_v1.lua
var maintenanceLibrary string

func installMaintenanceLibrary(ctx context.Context, c valkey.Client) error {
	err := c.Do(ctx, c.B().FunctionLoad().FunctionCode(maintenanceLibrary).Build()).Error()
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return err
	}
	return verifyMaintenanceLibrary(ctx, c)
}
func verifyMaintenanceLibrary(ctx context.Context, c valkey.Client) error {
	entries, err := c.Do(ctx, c.B().FunctionList().Libraryname("wr_queue_maintenance_v1").Withcode().Build()).ToArray()
	if err != nil {
		return err
	}
	if len(entries) != 1 {
		return ErrSchema
	}
	fields, err := entries[0].AsMap()
	if err != nil {
		return ErrSchema
	}
	code := fields["library_code"]
	actual, err := code.ToString()
	if err != nil || actual != maintenanceLibrary {
		return ErrSchema
	}
	return nil
}
