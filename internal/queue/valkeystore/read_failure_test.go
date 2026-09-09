// SPDX-License-Identifier: Apache-2.0
package valkeystore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"testing"

	valkey "github.com/valkey-io/valkey-go"
	"waiting-room/internal/queue/model"
)

func TestReadTransportFailurePreservesServerFailures(t *testing.T) {
	for _, err := range []error{io.EOF, io.ErrUnexpectedEOF, net.ErrClosed, valkey.ErrClosing, context.Canceled, context.DeadlineExceeded,
		&net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}} {
		if !readTransportFailure(fmt.Errorf("transport: %w", err)) {
			t.Errorf("read transport failure was not recognized: %v", err)
		}
	}
	for _, err := range []error{nil, ErrSchema, model.ErrUnavailable, errors.New("WR_UNAVAILABLE"), errors.New("WR_SCHEMA"), errors.New("WR_FENCED"), errors.New("WRONGTYPE"), errors.New("unexpected response")} {
		if readTransportFailure(err) {
			t.Errorf("server/schema failure bypassed protection: %v", err)
		}
	}
}
