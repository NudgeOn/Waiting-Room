// SPDX-License-Identifier: Apache-2.0
// Package adminlab retains a strictly loopback, disposable test entrypoint.
package adminlab

import (
	"context"
	"crypto/tls"
	"errors"
	"io/fs"
	"net"
	"net/http"

	"waiting-room/internal/adminauth"
	"waiting-room/internal/adminauth/pgstore"
	"waiting-room/internal/adminserver"
)

func Certificate() (tls.Certificate, error) { return adminserver.Certificate() }

func Handler(store *pgstore.Store, passwords *adminauth.PasswordHasher, fingerprint [32]byte, ui fs.FS, origin string, setup, totp bool) (http.Handler, error) {
	return adminserver.Handler(store, passwords, fingerprint, ui, adminserver.Options{Origin: origin, Setup: setup, TOTP: totp, KeyID: "lab"})
}

func Serve(ctx context.Context, listener net.Listener, cert tls.Certificate, handler http.Handler) error {
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok || !addr.IP.IsLoopback() {
		return errors.New("loopback listener required")
	}
	return adminserver.Serve(ctx, listener, cert, handler)
}
