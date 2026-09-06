// SPDX-License-Identifier: Apache-2.0
// Package processlab runs isolated local lab roles, not production services.
package processlab

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"waiting-room/internal/lab"
	"waiting-room/internal/queue/valkeystore"
)

var ErrRole = errors.New("local process role failed (details withheld)")

type coordinatorInput struct {
	Address      string `json:"address"`
	Namespace    string `json:"namespace"`
	ConfigTrust  []byte `json:"configTrust"`
	SignedConfig []byte `json:"signedConfig"`
}
type gatewayInput struct {
	Coordinator  string `json:"coordinator"`
	Origin       string `json:"origin"`
	Service      string `json:"service"`
	Public       []byte `json:"public"`
	ReturnKey    []byte `json:"returnKey"`
	Installation string `json:"installation"`
	ConfigTrust  []byte `json:"configTrust"`
	SignedConfig []byte `json:"signedConfig"`
}

// Private readiness IPC. Never serialize this type to the user's terminal.
type ready struct {
	URL     string `json:"url"`
	PID     int    `json:"pid"`
	Service string `json:"service,omitempty"`
	Public  []byte `json:"public,omitempty"`
}

func configLine(in *bufio.Reader, dst any) error {
	line, err := in.ReadSlice('\n') // Reader capacity bounds the entire frame.
	if err != nil {
		return ErrRole
	}
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) < 2 || trimmed[0] != '{' || trimmed[len(trimmed)-1] != '}' {
		return ErrRole
	}
	d := json.NewDecoder(bytes.NewReader(line))
	d.DisallowUnknownFields()
	if d.Decode(dst) != nil || d.Decode(new(any)) != io.EOF {
		return ErrRole
	}
	return nil
}

// RunChild requires dedicated stdin/stdout pipes. EOF on the parent's control
// pipe shuts the role down, including if the supervisor is killed unexpectedly.
func RunChild(ctx context.Context, role string, input, output *os.File) error {
	for _, f := range []*os.File{input, output} {
		info, err := f.Stat()
		if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
			return ErrRole
		}
	}
	return runRole(ctx, role, input, output)
}

func runRole(parent context.Context, role string, input io.Reader, output io.Writer) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	reader := bufio.NewReaderSize(input, 16384)
	var h http.Handler
	r := ready{PID: os.Getpid()}
	var store *valkeystore.Store
	var pumpDone chan struct{}
	switch role {
	case "origin":
		var in struct{}
		if configLine(reader, &in) != nil {
			return ErrRole
		}
		var count atomic.Int64
		h = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if req.URL.Path == "/__lab/count" {
				_ = json.NewEncoder(w).Encode(map[string]int64{"count": count.Load()})
				return
			}
			count.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"origin": "process-lab", "path": req.URL.RequestURI(), "pid": os.Getpid()})
		})
	case "coordinator":
		var in coordinatorInput
		if configLine(reader, &in) != nil || in.Address != "127.0.0.1:16379" || !regexp.MustCompile(`^wr:lab:process-[a-f0-9]{32}$`).MatchString(in.Namespace) {
			return ErrRole
		}
		cfg := coordinatorConfig()
		trust := coordinatorTrust(in)
		if _, err := trust.Current(); err != nil {
			return ErrRole
		}
		start, stop := context.WithTimeout(ctx, 5*time.Second)
		var err error
		store, err = valkeystore.OpenRoom(start, in.Address, in.Namespace, "room", cfg, valkeystore.StandardInstallation())
		stop()
		if err != nil {
			return ErrRole
		}
		defer store.Close()
		c, err := lab.NewCoordinator(store, cfg)
		if err != nil {
			return ErrRole
		}
		h = trust.Handler(c.Handler())
		r.Service = c.ServiceKey
		r.Public = c.Public
		pumpDone = make(chan struct{})
		go func() {
			defer close(pumpDone)
			tick := time.NewTicker(50 * time.Millisecond)
			defer tick.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
					if _, err := trust.Current(); err != nil {
						continue
					}
					call, done := context.WithTimeout(ctx, 2*time.Second)
					_, _ = store.Promote(call, 128)
					done()
				}
			}
		}()
		defer func() { cancel(); <-pumpDone }()
	case "gateway":
		var in gatewayInput
		if configLine(reader, &in) != nil || len(in.Public) != ed25519.PublicKeySize || len(in.Service) != 43 {
			return ErrRole
		}
		trust := gatewayTrust(in)
		if _, err := trust.Current(); err != nil {
			h = trust.Handler(nil)
			break
		}
		var err error
		h, err = lab.NewGatewayWithReturnKey(in.Coordinator, in.Origin, in.Service, in.Public, "calm", in.ReturnKey)
		if err != nil {
			return ErrRole
		}
		h = trust.Handler(h)
	default:
		return ErrRole
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return ErrRole
	}
	r.URL = "http://" + listener.Addr().String()
	server := &http.Server{Handler: h, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16384, ErrorLog: log.New(io.Discard, "", 0)}
	defer server.Close()
	// Any bytes after configuration are invalid control traffic; EOF is normal.
	go func() { _, _ = reader.ReadByte(); cancel() }()
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	if json.NewEncoder(output).Encode(r) != nil {
		return ErrRole
	}
	select {
	case <-ctx.Done():
	case <-done:
		return ErrRole
	}
	shutdown, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	if server.Shutdown(shutdown) != nil {
		_ = server.Close()
	}
	return nil
}

func publicURL(raw string) bool {
	return strings.HasPrefix(raw, "http://127.0.0.1:") && validAddress(strings.TrimPrefix(raw, "http://"))
}
func validAddress(address string) bool {
	host, port, err := net.SplitHostPort(address)
	if err != nil || host != "127.0.0.1" {
		return false
	}
	n, err := strconv.Atoi(port)
	return err == nil && n > 0 && n <= 65535
}
