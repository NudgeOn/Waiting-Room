// SPDX-License-Identifier: Apache-2.0
package processlab

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

type child struct {
	cmd    *exec.Cmd
	input  io.WriteCloser
	output io.ReadCloser
	done   chan struct{}
	once   sync.Once
	ready  ready
}

func (p *child) close() {
	p.once.Do(func() {
		_ = p.input.Close()
		select {
		case <-p.done:
		case <-time.After(4 * time.Second):
			_ = p.cmd.Process.Kill()
			<-p.done
		}
		_ = p.output.Close()
	})
}

func startChild(ctx context.Context, binary, role string, config any) (*child, error) {
	if ctx.Err() != nil || !filepath.IsAbs(binary) {
		return nil, ErrRole
	}
	cmd := exec.Command(binary, "child", role)
	// No inherited database credentials, proxy settings or signing material.
	// Bound local-role Go parallelism independently of host CPU count. Four lab
	// children must not each assume ownership of the entire developer machine.
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C", "GORACE=atexit_sleep_ms=0", "GOMAXPROCS=2"}
	cmd.Stderr = io.Discard
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, ErrRole
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		_ = in.Close()
		return nil, ErrRole
	}
	if cmd.Start() != nil {
		_ = in.Close()
		_ = out.Close()
		return nil, ErrRole
	}
	p := &child{cmd: cmd, input: in, output: out, done: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(p.done) }()
	ok := false
	defer func() {
		if !ok {
			p.close()
		}
	}()
	if json.NewEncoder(in).Encode(config) != nil {
		return nil, ErrRole
	}
	read := make(chan error, 1)
	go func() { read <- configLine(bufio.NewReaderSize(out, 4096), &p.ready) }()
	select {
	case <-ctx.Done():
		return nil, ErrRole
	case <-time.After(8 * time.Second):
		return nil, ErrRole
	case err := <-read:
		if err != nil {
			return nil, ErrRole
		}
	}
	if p.ready.PID != cmd.Process.Pid || !publicURL(p.ready.URL) {
		return nil, ErrRole
	}
	if role == "coordinator" {
		if len(p.ready.Service) != 43 || len(p.ready.Public) != 32 {
			return nil, ErrRole
		}
	} else if p.ready.Service != "" || len(p.ready.Public) != 0 {
		return nil, ErrRole
	}
	ok = true
	return p, nil
}

type Cluster struct {
	children            []*child
	gatewayConfig       gatewayInput
	gateways            []string
	coordinator, origin string
	namespace           string // private, randomly generated fixture ownership
	closed              chan struct{}
	once                sync.Once
}
type Summary struct {
	SupervisorPID int      `json:"supervisorPid"`
	ChildPIDs     []int    `json:"childPids"`
	Gateways      []string `json:"gateways"`
	Scope         string   `json:"scope"`
}

// Start starts four separate OS children. Only the Coordinator generates and
// retains the admission private key; Gateways receive public verification data.
func Start(ctx context.Context, binary string) (*Cluster, error) {
	c := &Cluster{closed: make(chan struct{})}
	ok := false
	defer func() {
		if !ok {
			c.Close()
		}
	}()
	start := func(role string, config any) (*child, error) {
		p, err := startChild(ctx, binary, role, config)
		if err == nil {
			c.children = append(c.children, p)
		}
		return p, err
	}
	origin, err := start("origin", struct{}{})
	if err != nil {
		return nil, err
	}
	c.origin = origin.ready.URL
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return nil, ErrRole
	}
	c.namespace = "wr:lab:process-" + hex.EncodeToString(nonce)
	coordInput, err := signCoordinatorConfig(coordinatorInput{Address: "127.0.0.1:16379", Namespace: c.namespace}, time.Now(), 24*time.Hour)
	if err != nil {
		return nil, ErrRole
	}
	coord, err := start("coordinator", coordInput)
	if err != nil {
		return nil, err
	}
	c.coordinator = coord.ready.URL
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, ErrRole
	}
	c.gatewayConfig = gatewayInput{Coordinator: c.coordinator, Origin: c.origin, Service: coord.ready.Service, Public: coord.ready.Public, ReturnKey: key}
	c.gatewayConfig.Installation = "process-" + hex.EncodeToString(nonce)
	c.gatewayConfig, err = signGatewayConfig(c.gatewayConfig, time.Now(), 24*time.Hour)
	if err != nil {
		return nil, ErrRole
	}
	for range 2 {
		g, err := start("gateway", c.gatewayConfig)
		if err != nil {
			return nil, err
		}
		c.gateways = append(c.gateways, g.ready.URL)
	}
	ok = true
	go func() {
		select {
		case <-ctx.Done():
			c.Close()
		case <-c.closed:
		}
	}()
	return c, nil
}

func (c *Cluster) Close() {
	c.once.Do(func() {
		close(c.closed)
		for _, p := range c.children {
			_ = p.input.Close()
		}
		for i := len(c.children) - 1; i >= 0; i-- {
			c.children[i].close()
		}
	})
}
func (c *Cluster) Gateways() []string { return append([]string(nil), c.gateways...) }
func (c *Cluster) Summary() Summary {
	s := Summary{SupervisorPID: os.Getpid(), Gateways: c.Gateways(), Scope: "loopback-process-lab-not-production"}
	for _, p := range c.children {
		s.ChildPIDs = append(s.ChildPIDs, p.ready.PID)
	}
	return s
}
func (c *Cluster) Failure() <-chan struct{} {
	failed := make(chan struct{})
	var once sync.Once
	for _, p := range c.children {
		go func(p *child) {
			select {
			case <-p.done:
				once.Do(func() { close(failed) })
			case <-c.closed:
			}
		}(p)
	}
	return failed
}
