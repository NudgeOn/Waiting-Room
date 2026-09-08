// SPDX-License-Identifier: Apache-2.0
package trafficlab

import (
	"context"
	"crypto/rand"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	valkey "github.com/valkey-io/valkey-go"
	"waiting-room/internal/control"
	"waiting-room/internal/lab"
	"waiting-room/internal/queue/model"
	"waiting-room/internal/queue/valkeystore"
)

const namespace = "wr:lab:traffic"

// Only these eleven dedicated fixture keys are ever deleted. The executor has
// one process-wide slot and a separate Valkey ACL with no wr:runtime access.
func fixtureKeys() []string {
	var keys []string
	for _, suffix := range []string{"meta", "tickets", "waiting", "expiry", "leases", "rate", "idempotency", "idem-expiry"} {
		keys = append(keys, namespace+"{"+lab.Room+":1}:"+suffix)
	}
	for _, suffix := range []string{"meta", "visitors", "idempotency"} {
		keys = append(keys, namespace+"{installation:1}:"+suffix)
	}
	return keys
}

type localServer struct {
	url    string
	server *http.Server
	once   sync.Once
}

func serveLocal(h http.Handler) (*localServer, error) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, ErrRun
	}
	s := &http.Server{Handler: h, ReadHeaderTimeout: time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second, IdleTimeout: 5 * time.Second, ErrorLog: log.New(io.Discard, "", 0)}
	go s.Serve(l)
	return &localServer{"http://" + l.Addr().String(), s, sync.Once{}}, nil
}
func (s *localServer) close() { s.once.Do(func() { _ = s.server.Close() }) }

type fixture struct {
	store    *valkeystore.Store
	client   valkey.Client
	config   model.Config
	coord    *localServer
	servers  []*localServer
	gateways []string
	reached  atomic.Int32
	leaked   atomic.Bool
}

func openFixture(ctx context.Context, options valkey.ClientOption) (*fixture, error) {
	options.DisableCache = true
	options.DisableRetry = true
	options.ForceSingleClient = true
	client, err := valkey.NewClient(options)
	if err != nil {
		return nil, ErrRun
	}
	f := &fixture{client: client}
	ok := false
	defer func() {
		if !ok {
			f.close()
		}
	}()
	if options.Username == "wr_traffic" {
		// Refuse to execute with an accidentally broadened deployment ACL.
		err := client.Do(ctx, client.B().Exists().Key("wr:runtime:local{installation:1}:meta").Build()).Error()
		if err == nil || !strings.HasPrefix(err.Error(), "NOPERM") {
			return nil, ErrRun
		}
	}
	if client.Do(ctx, client.B().Del().Key(fixtureKeys()...).Build()).Error() != nil {
		return nil, ErrRun
	}
	f.config = model.DefaultConfig()
	f.config.LeaseCap = 3
	f.config.Rate = 6
	f.config.AdmissionTTL = 60000
	f.store, err = valkeystore.OpenRuntimeRoom(ctx, options, namespace, lab.Room, f.config, valkeystore.StandardInstallation())
	if err != nil {
		return nil, ErrRun
	}
	if _, err = f.store.Configure(ctx, f.config, 1, control.Runtime{Revision: 1, Epoch: 1, Mode: "HOLD"}); err != nil {
		return nil, ErrRun
	}
	coordinator, err := lab.NewCoordinator(f.store, f.config)
	if err != nil {
		return nil, ErrRun
	}
	f.coord, err = serveLocal(coordinator.Handler())
	if err != nil {
		return nil, err
	}
	f.servers = append(f.servers, f.coord)
	origin, err := serveLocal(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-WR-Service") != "" || r.Header.Get("X-Waiting-Room-Admission") != "" {
			f.leaked.Store(true)
		}
		for _, cookie := range r.Cookies() {
			if len(cookie.Name) >= 7 && cookie.Name[:7] == "wr_dev_" {
				f.leaked.Store(true)
			}
		}
		f.reached.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sample":"traffic-lab"}`))
	}))
	if err != nil {
		return nil, err
	}
	f.servers = append(f.servers, origin)
	key := make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		return nil, ErrRun
	}
	for range 2 {
		h, e := lab.NewGatewayWithReturnKey(f.coord.url, origin.url, coordinator.ServiceKey, coordinator.Public, "calm", key)
		if e != nil {
			return nil, ErrRun
		}
		g, e := serveLocal(h)
		if e != nil {
			return nil, e
		}
		f.servers = append(f.servers, g)
		f.gateways = append(f.gateways, g.url)
	}
	f.expire(ctx)
	ok = true
	return f, nil
}
func (f *fixture) expire(ctx context.Context) {
	for _, key := range fixtureKeys() {
		_ = f.client.Do(ctx, f.client.B().Pexpire().Key(key).Milliseconds(600000).Build()).Error()
	}
}
func (f *fixture) close() {
	for i := len(f.servers) - 1; i >= 0; i-- {
		f.servers[i].close()
	}
	if f.store != nil {
		f.store.Close()
	}
	if f.client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = f.client.Do(ctx, f.client.B().Del().Key(fixtureKeys()...).Build()).Error()
		f.client.Close()
	}
}
