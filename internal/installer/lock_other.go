//go:build !linux && !darwin

// SPDX-License-Identifier: Apache-2.0
package installer

import "errors"

func lockDirectory(string) (func(), error) {
	return nil, errors.New("local runtime installation currently supports Linux and macOS")
}
