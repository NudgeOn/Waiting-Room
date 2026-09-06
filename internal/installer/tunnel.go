// SPDX-License-Identifier: Apache-2.0
package installer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

func (e engine) setup(ctx context.Context, dir string, s installation) error {
	listener, err := net.Listen("tcp4", "127.0.0.1:19444")
	if err != nil {
		return errors.New("setup port 19444 is in use; no token was rotated")
	}
	defer listener.Close()
	if err = e.step(ctx, dir, s, s.Image, "Issuing a private first-admin token (15 minutes)", "run", "--rm", "--no-deps", "--pull", "never", "bootstrap"); err != nil {
		return err
	}
	if _, err = fmt.Fprintln(e.out, "One-time setup token (keep this terminal private):"); err != nil {
		return err
	}
	if err = e.printToken(ctx, dir, s); err != nil {
		return err
	}
	if _, err = fmt.Fprintln(e.out, "Open https://127.0.0.1:19444/setup and complete first-admin registration.\nThe certificate is local and self-signed. Ctrl-C closes this setup tunnel.\nAfter registration, use https://127.0.0.1:19443."); err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer stop()
	slots := make(chan struct{}, 16)
	var active sync.WaitGroup
	defer active.Wait()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return errors.New("setup tunnel stopped")
		}
		select {
		case slots <- struct{}{}:
			active.Add(1)
			go func() {
				defer active.Done()
				defer func() { <-slots }()
				defer conn.Close()
				e.tunnelConnection(ctx, dir, s, conn)
			}()
		default:
			_ = conn.Close()
		}
	}
}

func (e engine) tunnelConnection(parent context.Context, dir string, s installation, conn net.Conn) {
	ctx, cancel := context.WithTimeout(parent, 90*time.Second)
	defer cancel()
	closeConn := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer closeConn()
	_ = conn.SetDeadline(time.Now().Add(90 * time.Second))
	args := []string{"compose", "--project-name", s.Project, "--project-directory", dir, "--env-file", os.DevNull, "--file", filepath.Join(dir, "compose.yaml"), "exec", "-T", "control", "/wr-control", "tunnel"}
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir, cmd.Env = dir, dockerEnvironment(e.environ, s.Image)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = conn, conn, io.Discard
	cmd.WaitDelay = time.Second
	_ = cmd.Run()
}
