// SPDX-License-Identifier: Apache-2.0
package valkeystore

import (
	"context"
	"errors"
	"io"
	"net"

	valkey "github.com/valkey-io/valkey-go"
)

// A lost read reply cannot hide a partial write. Limit this exception to
// transport errors: server invariants, schema errors and invalid responses
// must still fail closed. Subsequent calls verify primary identity or fencing.
func readTransportFailure(err error) bool {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, net.ErrClosed) || errors.Is(err, valkey.ErrClosing) ||
		errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var networkError net.Error
	return errors.As(err, &networkError)
}
