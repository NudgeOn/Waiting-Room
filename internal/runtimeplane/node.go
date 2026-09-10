// SPDX-License-Identifier: Apache-2.0
package runtimeplane

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	valkey "github.com/valkey-io/valkey-go"
	"waiting-room/internal/adminauth/pgstore"
	"waiting-room/internal/configtrust"
	"waiting-room/internal/control"
	"waiting-room/internal/lab"
	"waiting-room/internal/localcontrol"
	"waiting-room/internal/publicguard"
	"waiting-room/internal/queue/valkeystore"
	"waiting-room/internal/trafficlab"
	"waiting-room/internal/waiting"
)

type arrivalWindow struct {
	mu            sync.Mutex
	started, last int64
	counts        [300]int
	seconds       [300]int64
}

func (a *arrivalWindow) observe(now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	sec := now.Unix()
	if sec < a.last {
		a.started = sec
		a.counts = [300]int{}
		a.seconds = [300]int64{}
	}
	a.last = sec
	if a.started == 0 {
		a.started = sec
	}
	i := sec % 300
	if a.seconds[i] != sec {
		a.seconds[i] = sec
		a.counts[i] = 0
	}
	if a.counts[i] < 333333 {
		a.counts[i]++
	}
}
func (a *arrivalWindow) snapshot(now time.Time) (int, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	sec := now.Unix()
	if sec < a.last {
		return 0, false
	}
	total := 0
	for i, s := range a.seconds {
		if s > sec-300 && s <= sec {
			total += a.counts[i]
		}
	}
	return total, a.started > 0 && sec-a.started >= 300
}

type roomHandler struct {
	room            control.Room
	runtime         control.Runtime
	handler, origin http.Handler
	transport       *http.Transport
	client          *http.Client
	arrivals        *arrivalWindow
	httpErrors      *httpErrorWindow
}
type Node struct {
	diagnostics syncDiagnostic
	identity    localcontrol.NodeIdentity
	gate        *configtrust.Gate
	disk        *configtrust.FileStore
	client      *http.Client
	transport   *http.Transport
	mu          sync.RWMutex
	generation  uint64
	delivery    control.Delivery
	rooms       map[string]*roomHandler
	guards      map[string]*publicguard.Guard
	stores      map[string]*valkeystore.Store
	raw         []byte
	traffic     trafficlab.Execute
}

func OpenNode(n localcontrol.NodeIdentity, dir string) (*Node, error) {
	if n.Node != "gateway" && n.Node != "coordinator" {
		return nil, errors.New("invalid data role")
	}
	disk, err := configtrust.NewFileStore(dir)
	if err != nil {
		return nil, err
	}
	trust := map[string]ed25519.PublicKey{"config-v1": n.ConfigPublic}
	if n.Keys != nil {
		trust = n.Keys.ConfigTrust()
	}
	gate, err := configtrust.Open(trust, n.Installation, 1, control.ValidateDelivery, disk)
	if err != nil {
		disk.Close()
		return nil, err
	}
	tr, err := internalTransport(n)
	if err != nil {
		disk.Close()
		return nil, err
	}
	node := &Node{identity: n, gate: gate, disk: disk, client: &http.Client{Transport: tr, Timeout: 4 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, transport: tr, rooms: map[string]*roomHandler{}, stores: map[string]*valkeystore.Store{}, guards: map[string]*publicguard.Guard{}}
	if n.Node == "coordinator" {
		node.traffic = trafficlab.NewExecutor(valkey.ClientOption{InitAddress: []string{"valkey:6379"}, Username: "wr_traffic", Password: localcontrol.TrafficQueuePassword(n.ValkeyPassword)})
	}
	return node, nil
}
func (n *Node) Close() {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, s := range n.stores {
		s.Close()
	}
	for _, g := range n.guards {
		g.Close()
	}
	for _, r := range n.rooms {
		if r.transport != nil {
			r.transport.CloseIdleConnections()
		}
	}
	n.transport.CloseIdleConnections()
	n.disk.Close()
}
func (n *Node) validLocked() bool {
	s, err := n.gate.Current()
	return err == nil && s.Generation == n.generation && n.generation > 0
}
func (n *Node) Ready() bool { n.mu.RLock(); defer n.mu.RUnlock(); return n.validLocked() }
func targetFor(c control.Config, room control.Room, target string) bool {
	if len(target) == 0 || len(target) > 2048 || strings.HasPrefix(target, "//") {
		return false
	}
	for _, r := range target {
		if r < 32 || r == 127 || r == '\\' {
			return false
		}
	}
	u, err := url.ParseRequestURI(target)
	if err != nil || u.IsAbs() || u.Host != "" || u.Fragment != "" {
		return false
	}
	m := c.MatchURL(room.Hostname, u)
	return m.Decision == "protected" && m.RoomID == room.ID
}
func (n *Node) apply(ctx context.Context, s configtrust.Snapshot) error {
	var d control.Delivery
	if control.DecodeExact(s.Payload, &d) != nil || d.Validate() != nil {
		return control.ErrInvalid
	}
	// A routine poll usually returns the already applied generation. Taking a
	// writer lock here would queue all subsequent requests behind existing
	// network I/O because RWMutex gives pending writers priority over readers.
	n.mu.RLock()
	unchanged := s.Generation == n.generation
	n.mu.RUnlock()
	if unchanged {
		return nil
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if s.Generation == n.generation {
		return nil
	}
	next := map[string]*roomHandler{}
	for i, room := range d.Config.Rooms {
		runtime := d.Runtimes[i].Runtime
		b := lab.Binding{Keys: n.identity.Keys, SourceKey: n.identity.SourceKey, Room: room.PublicID, Audience: n.identity.Installation + ":" + room.PublicID, Kid: "admission-v1", Epoch: runtime.Epoch, ValidTarget: func(target string) bool { return targetFor(d.Config, room, target) }}
		r := &roomHandler{room: room, runtime: runtime}
		if n.identity.Node == "coordinator" {
			c := runtime.QueueConfig(d.Config.Profile, room)
			storeKey := fmt.Sprintf("%s:%d", room.PublicID, runtime.Epoch)
			store := n.stores[storeKey]
			if store == nil {
				installation := valkeystore.StandardInstallation()
				if d.Config.Profile == "high-scale-100k" {
					installation = valkeystore.InstallationConfig{Profile: "high", VisitorCap: 100000, IdempotencyCap: 200000}
				}
				var err error
				store, err = valkeystore.OpenRecoveryRoom(ctx, valkey.ClientOption{InitAddress: []string{"valkey:6379"}, Username: "wr_coordinator", Password: n.identity.ValkeyPassword}, "wr:runtime:local", room.PublicID, c, installation, runtime.Epoch, runtime.RecoveryUntil)
				if err != nil {
					return err
				}
				n.stores[storeKey] = store
			}
			if _, err := store.Configure(ctx, c, d.Config.Revision, runtime); err != nil {
				return err
			}
			coord, err := lab.NewBoundCoordinator(store, c, b, n.identity.AdmissionPrivate, n.identity.ReplayKey, n.identity.Service)
			if err != nil {
				return err
			}
			guard := n.guards[storeKey]
			if guard == nil {
				cap := 10000
				if d.Config.Profile == "high-scale-100k" {
					cap = 100000
				}
				guard, err = publicguard.Open(ctx, valkey.ClientOption{InitAddress: []string{"valkey:6379"}, Username: "wr_coordinator", Password: n.identity.ValkeyPassword}, "wr:runtime:local", room.PublicID, runtime.Epoch, cap)
				if err != nil {
					return err
				}
				n.guards[storeKey] = guard
			}
			coord.Guard = guard.Check
			r.handler = coord.Handler()
		} else {
			tr, err := OriginTransport(room.Origin, n.identity)
			if err != nil {
				return err
			}
			r.transport = tr
			r.client = &http.Client{Transport: tr, Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			r.arrivals = &arrivalWindow{started: time.Now().Unix()}
			r.httpErrors = httpErrorsFor(n.rooms[room.PublicID], room, time.Now())
			if old := n.rooms[room.PublicID]; old != nil && old.room.Origin == room.Origin && old.room.Hostname == room.Hostname {
				r.arrivals = old.arrivals
			}
			r.handler, err = lab.NewBoundGateway("https://coordinator:19446", room.Origin, n.identity.Service, n.identity.AdmissionPublic, room.Theme, n.identity.ReturnKey, b, net.JoinHostPort(room.Hostname, "20443"), n.transport, tr, runtime.Mode == "OFF", func() { r.arrivals.observe(time.Now()) }, runtime.Mode)
			if err != nil {
				return err
			}
			upstream, _ := url.Parse(room.Origin)
			r.origin = &httputil.ReverseProxy{Transport: tr, Rewrite: func(p *httputil.ProxyRequest) {
				p.SetURL(upstream)
				stripOriginHeaders(p.Out.Header)
				cookies := p.Out.Cookies()
				p.Out.Header.Del("Cookie")
				for _, c := range cookies {
					if !strings.HasPrefix(c.Name, "__Host-wr") && !strings.HasPrefix(c.Name, "wr_dev_") {
						p.Out.AddCookie(c)
					}
				}
			}, ErrorHandler: func(w http.ResponseWriter, r *http.Request, e error) { unavailable(w) }}
		}
		next[room.PublicID] = r
	}
	for _, old := range n.rooms {
		if old.transport != nil {
			old.transport.CloseIdleConnections()
		}
	}
	activeStores := map[string]bool{}
	for i, room := range d.Config.Rooms {
		activeStores[fmt.Sprintf("%s:%d", room.PublicID, d.Runtimes[i].Runtime.Epoch)] = true
	}
	for key, store := range n.stores {
		if !activeStores[key] {
			store.Close()
			delete(n.stores, key)
		}
	}
	for key, guard := range n.guards {
		if !activeStores[key] {
			guard.Close()
			delete(n.guards, key)
		}
	}
	n.rooms = next
	n.delivery = d
	n.generation = s.Generation
	return nil
}
func (n *Node) sync(ctx context.Context) {
	stage, code := "", ""
	defer func() {
		n.mu.RLock()
		generation := n.generation
		n.mu.RUnlock()
		if line := n.diagnostics.observe(n.identity.Node, stage, code, generation, time.Now()); line != "" {
			log.Print(line)
		}
	}()

	method, endpoint := "GET", "https://control:19445/internal/v1/config"
	var body io.Reader
	if recovery, needed := n.gate.RecoveryRequest(); needed {
		method, endpoint = "POST", endpoint+"/clock-recovery"
		raw, _ := json.Marshal(recovery)
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		stage, code = "fetch", syncErrorCode(err)
		return
	}
	if method == "POST" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := n.client.Do(req)
	if err != nil {
		stage, code = "fetch", syncErrorCode(err)
	} else {
		raw, e := io.ReadAll(io.LimitReader(resp.Body, 65537))
		resp.Body.Close()
		if e == nil && resp.StatusCode == 200 && len(raw) <= 65536 {
			if e = n.gate.Apply(raw); e == nil {
				n.raw = raw
			} else {
				stage, code = "verify", syncErrorCode(e)
			}
		} else {
			stage, code = "fetch", syncHTTPCode(resp.StatusCode, e)
		}
	}
	// A valid persisted snapshot survives Control unavailability; expiry still
	// closes requests and promotions. No unsigned draft or default is installed.
	s, err := n.gate.Current()
	if err != nil {
		stage, code = "current", syncErrorCode(err)
		if reason := n.gate.FailureReason(); reason != "" {
			code = reason
		}
		return
	}
	if err = n.apply(ctx, s); err != nil {
		stage, code = "apply", syncErrorCode(err)
		return
	}
	if len(n.raw) == 0 {
		n.raw, _ = n.disk.Load()
	}
	sum := sha256.Sum256(n.raw)
	ack := pgstore.NodeAck{Generation: int64(s.Generation), Digest: hex.EncodeToString(sum[:]), Rooms: []pgstore.RoomMetrics{}}
	if n.identity.Keys != nil {
		ack.KeyGeneration = n.identity.Keys.Generation
		ack.KeyDigest = n.identity.Keys.Digest()
	}
	if n.identity.Node == "gateway" {
		var ok bool
		ack.Rooms, ok = n.gatewayMetrics(ctx, s.Generation)
		if !ok {
			stage, code = "metrics", "unavailable"
			return
		}
	} else {
		var ok bool
		ack.Rooms, ok = n.coordinatorMetrics(ctx)
		if !ok {
			stage, code = "metrics", "unavailable"
			return
		}
	}
	raw, _ := json.Marshal(ack)
	req, err = http.NewRequestWithContext(ctx, "POST", "https://control:19445/internal/v1/ack", bytes.NewReader(raw))
	if err != nil {
		stage, code = "ack", syncErrorCode(err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err = n.client.Do(req)
	if err != nil {
		stage, code = "ack", syncErrorCode(err)
	} else {
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			stage, code = "ack", syncHTTPCode(resp.StatusCode, nil)
		}
	}
}

// Room handlers are immutable after publication. Snapshot references while
// locked, then perform bounded health I/O without blocking config application.
// Control rejects stale-generation ACKs if a newer snapshot wins meanwhile.
func (n *Node) gatewayMetrics(ctx context.Context, generation uint64) ([]pgstore.RoomMetrics, bool) {
	n.mu.RLock()
	if !n.validLocked() || n.generation != generation {
		n.mu.RUnlock()
		return nil, false
	}
	rooms := make([]*roomHandler, 0, len(n.delivery.Config.Rooms))
	for _, room := range n.delivery.Config.Rooms {
		rooms = append(rooms, n.rooms[room.PublicID])
	}
	n.mu.RUnlock()
	return probeGatewayRooms(ctx, rooms), true
}

func probeGatewayRooms(ctx context.Context, rooms []*roomHandler) []pgstore.RoomMetrics {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	metrics := make([]pgstore.RoomMetrics, len(rooms))
	jobs := make(chan int)
	var workers sync.WaitGroup
	for range min(8, len(rooms)) {
		workers.Go(func() {
			for i := range jobs {
				r := rooms[i]
				m := pgstore.RoomMetrics{RoomID: r.room.ID, Revision: r.runtime.Revision, Epoch: r.runtime.Epoch, Mode: r.runtime.Mode}
				// Cancellation is unverified/unhealthy, never a stale success.
				if ctx.Err() == nil {
					m.OriginHealthy = healthy(ctx, r.client, r.room.HealthURL)
				}
				m.ArrivalsFiveMinutes, m.ArrivalWindowReady = r.arrivals.snapshot(time.Now())
				if r.httpErrors != nil {
					count, ready := r.httpErrors.snapshot(time.Now())
					m.HTTP5xxLastMinute, m.HTTP5xxWindowReady = &count, ready
				}
				metrics[i] = m
			}
		})
	}
	for i := range rooms {
		jobs <- i
	}
	close(jobs)
	workers.Wait()
	return metrics
}

func (n *Node) coordinatorMetrics(ctx context.Context) ([]pgstore.RoomMetrics, bool) {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if !n.validLocked() {
		return nil, false
	}
	metrics := make([]pgstore.RoomMetrics, 0, len(n.delivery.Config.Rooms))
	for i, room := range n.delivery.Config.Rooms {
		runtime := n.delivery.Runtimes[i].Runtime
		m := pgstore.RoomMetrics{RoomID: room.ID, Revision: runtime.Revision, Epoch: runtime.Epoch, Mode: runtime.Mode}
		result, e := n.stores[fmt.Sprintf("%s:%d", room.PublicID, runtime.Epoch)].Metrics(ctx)
		if e != nil || result.Metrics == nil {
			return nil, false
		}
		q := result.Metrics
		m.Mode = q.Mode
		m.Revision = q.Revision
		m.Epoch = q.Epoch
		m.Waiting = q.Waiting
		m.Ready = q.Ready
		m.Leases = q.Leases
		m.Rate = q.Rate
		m.RecoveryUntil = q.RecoveryUntil
		m.RecoveryFence = q.RecoveryFence
		m.RecoveryReason = q.RecoveryReason
		m.RecoveryValidation = q.RecoveryValidation
		metrics = append(metrics, m)
	}
	return metrics, true
}
func (n *Node) Run(ctx context.Context) {
	done := make(chan struct{})
	if n.identity.Node == "coordinator" {
		go func() {
			defer close(done)
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					n.maintainRecovery(ctx)
					n.promote(ctx)
				}
			}
		}()
	} else {
		close(done)
	}
	defer func() { <-done }()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		// Stop scheduling on shutdown, but let an already submitted configuration
		// write finish within a fixed budget instead of creating uncertainty.
		call, cancel := context.WithTimeout(context.WithoutCancel(ctx), 12*time.Second)
		n.sync(call)
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (n *Node) maintainRecovery(ctx context.Context) {
	n.mu.RLock()
	defer n.mu.RUnlock()
	// This must run even while a newer signed configuration is pending apply;
	// otherwise a held store could prevent its own recovery/next-generation ACK.
	for _, store := range n.stores {
		if ctx.Err() != nil {
			return
		}
		call, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		_, _ = store.MaintainRecovery(call)
		cancel()
	}
}
func (n *Node) promote(ctx context.Context) {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if !n.validLocked() {
		return
	}
	for i, room := range n.delivery.Config.Rooms {
		if ctx.Err() != nil {
			return
		}
		call, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		_, _ = n.stores[fmt.Sprintf("%s:%d", room.PublicID, n.delivery.Runtimes[i].Runtime.Epoch)].Promote(call, 128)
		cancel()
	}
}
func (n *Node) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/internal/v1/traffic-lab" {
		serveTraffic(n.traffic, w, r)
		return
	}
	n.mu.RLock()
	defer n.mu.RUnlock()
	if !n.validLocked() {
		if (r.Method == "GET" || r.Method == "HEAD") && (r.Header.Get("Sec-Fetch-Dest") == "document" || strings.Contains(r.Header.Get("Accept"), "text/html")) {
			waiting.Problem(w, r, 503, "CONFIG_UNAVAILABLE")
			return
		}
		publicProblem(w, 503, "CONFIG_UNAVAILABLE")
		return
	}
	if n.identity.Node == "coordinator" {
		if Peer(r) != "gateway" || r.Host != "coordinator:19446" || len(r.Header.Values("X-WR-Room")) != 1 {
			publicProblem(w, 403, "FORBIDDEN")
			return
		}
		room := n.rooms[r.Header.Get("X-WR-Room")]
		if room == nil {
			publicProblem(w, 404, "NOT_FOUND")
			return
		}
		room.handler.ServeHTTP(w, r)
		return
	}
	host, port, err := net.SplitHostPort(r.Host)
	if err != nil || port != "20443" || r.TLS == nil || !control.ValidHostname(host) || r.URL.RawPath != "" || !control.ValidPath(r.URL.Path) {
		publicProblem(w, 400, "INVALID_REQUEST")
		return
	}
	if strings.HasPrefix(r.URL.Path, "/_wr/assets/") {
		known := false
		for _, room := range n.rooms {
			if room.room.Hostname == host {
				known = true
			}
		}
		if !known {
			publicProblem(w, 404, "NOT_FOUND")
			return
		}
		waiting.Asset(w, r)
		return
	}
	var selected *roomHandler
	if strings.HasPrefix(r.URL.Path, "/_wr/theme/") && strings.HasSuffix(r.URL.Path, ".css") {
		selected = n.rooms[strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/_wr/theme/"), ".css")]
	} else if strings.HasPrefix(r.URL.Path, "/_wr/theme/") && strings.HasSuffix(r.URL.Path, "/logo.png") {
		selected = n.rooms[strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/_wr/theme/"), "/logo.png")]
	} else if r.URL.Path == "/_wr/v1/tickets" {
		if r.Method != "POST" {
			w.Header().Set("Allow", "POST")
			publicProblem(w, 405, "METHOD_NOT_ALLOWED")
			return
		}
		raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
		var in struct {
			Target string `json:"target"`
		}
		if e != nil || control.DecodeExact(raw, &in) != nil {
			publicProblem(w, 400, "INVALID_REQUEST")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		for _, room := range n.rooms {
			if room.room.Hostname == host && targetFor(n.delivery.Config, room.room, in.Target) {
				selected = room
				break
			}
		}
	} else if strings.HasPrefix(r.URL.Path, "/_wr/v1/rooms/") || strings.HasPrefix(r.URL.Path, "/_wr/wait/") {
		parts := strings.Split(r.URL.Path, "/")
		idx := 4
		if strings.HasPrefix(r.URL.Path, "/_wr/wait/") {
			idx = 3
		}
		if len(parts) > idx {
			selected = n.rooms[parts[idx]]
		}
	} else {
		match := n.delivery.Config.MatchURL(host, r.URL)
		if match.Decision == "invalid" || match.Decision == "reserved" {
			publicProblem(w, 404, "NOT_FOUND")
			return
		}
		for _, room := range n.rooms {
			if room.room.ID == match.RoomID {
				selected = room
				break
			}
		}
		if match.Decision == "excluded" && selected != nil {
			serveMeasuredRoom(w, r, selected.origin, selected.httpErrors)
			return
		}
		if match.Decision == "unprotected" {
			var origin string
			for _, room := range n.rooms {
				if room.room.Hostname == host {
					if origin != "" && origin != room.room.Origin {
						publicProblem(w, 404, "NOT_FOUND")
						return
					}
					origin = room.room.Origin
					selected = room
				}
			}
			if selected != nil {
				selected.origin.ServeHTTP(w, r)
				return
			}
		}
	}
	if selected == nil || selected.room.Hostname != host {
		publicProblem(w, 404, "NOT_FOUND")
		return
	}
	serveMeasuredRoom(w, r, selected.handler, selected.httpErrors)
}
