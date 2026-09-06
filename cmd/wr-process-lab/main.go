// SPDX-License-Identifier: Apache-2.0
// A lab supervisor and its private child protocol, never a production entrypoint.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"waiting-room/internal/lab"
	"waiting-room/internal/processlab"
)

func main() {
	if run() != nil {
		fmt.Fprintln(os.Stderr, "process lab failed; no credentials or endpoint details logged")
		os.Exit(1)
	}
}
func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	args := os.Args[1:]
	if len(args) == 2 && args[0] == "child" {
		return processlab.RunChild(ctx, args[1], os.Stdin, os.Stdout)
	}
	if len(args) > 1 || (len(args) == 1 && args[0] != "--quick") {
		return processlab.ErrRole
	}
	binary, err := os.Executable()
	if err != nil {
		return processlab.ErrRole
	}
	c, err := processlab.Start(ctx, binary)
	if err != nil {
		return err
	}
	defer c.Close()
	if len(args) == 1 {
		check, done := context.WithTimeout(ctx, 20*time.Second)
		defer done()
		result, err := lab.Quick20(check, c.Gateways())
		if err != nil {
			return processlab.ErrRole
		}
		return json.NewEncoder(os.Stdout).Encode(struct {
			Processes processlab.Summary `json:"processes"`
			Quick     lab.QuickResult    `json:"quick20"`
		}{c.Summary(), result})
	}
	if json.NewEncoder(os.Stdout).Encode(c.Summary()) != nil {
		return processlab.ErrRole
	}
	select {
	case <-ctx.Done():
		return nil
	case <-c.Failure():
		if ctx.Err() != nil {
			return nil
		}
		return processlab.ErrRole
	}
}
