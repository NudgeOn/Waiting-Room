// SPDX-License-Identifier: Apache-2.0
package clockcheck

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"time"
)

func runChrony(ctx context.Context) ([]byte, error) {
	return runCommand(ctx, "/usr/bin/chronyc", []string{"-n", "-h", "127.0.0.1", "tracking"}, []string{"LC_ALL=C", "LANG=C", "PATH=/usr/bin:/bin"})
}

// Kept separate for subprocess boundary tests; callers cannot supply a command
// through the CLI, HTTP, plan input or environment.
func runCommand(ctx context.Context, binary string, args, env []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = env
	cmd.Stdin = nil
	output := &limitedOutput{}
	cmd.Stdout = output
	cmd.Stderr = io.Discard
	cmd.WaitDelay = 100 * time.Millisecond
	if err := cmd.Run(); err != nil || output.overflow {
		return nil, errors.New("local clock command failed")
	}
	return output.buffer.Bytes(), nil
}

type limitedOutput struct {
	buffer   bytes.Buffer
	overflow bool
}

func (w *limitedOutput) Write(p []byte) (int, error) {
	if len(p) > MaxOutputBytes-w.buffer.Len() {
		w.overflow = true
		return 0, errors.New("clock output limit exceeded")
	}
	return w.buffer.Write(p)
}
