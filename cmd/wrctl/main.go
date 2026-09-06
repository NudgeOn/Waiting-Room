// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"waiting-room/internal/installer"
	"waiting-room/internal/installplan"
	"waiting-room/internal/installplan/clockcheck"
	"waiting-room/internal/installplan/preview"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Fprintln(os.Stdout, "wrctl "+installer.Version)
		return
	}
	if len(os.Args) > 1 && installer.Handles(os.Args[1]) {
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		os.Exit(installer.Run(ctx, os.Args[1:], os.Stdout, os.Stderr))
	}
	if len(os.Args) == 2 && os.Args[1] == "preview" {
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		if err := preview.Run(ctx, os.DirFS("build/admin-ui"), os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// The CLI intentionally takes no input values, filenames or secrets in args.
// Reading stdin keeps this offline command composable without writing files.
func run(args []string, in io.Reader, out, stderr io.Writer) int {
	return runWithClock(args, in, out, stderr, clockcheck.Collect)
}

func runWithClock(args []string, in io.Reader, out, stderr io.Writer, collect func(context.Context, installplan.Input) (clockcheck.Report, error)) int {
	if len(args) != 1 || (args[0] != "plan" && args[0] != "estimate" && args[0] != "report" && args[0] != "doctor-clock") {
		fmt.Fprintln(stderr, "usage: wrctl plan|estimate|report|doctor-clock < non-secret-input.json (doctor-clock: local read-only chrony; local runtime: install/up/upgrade/setup; production apply unavailable)")
		return 2
	}
	var result any
	var err error
	exitCode := 0
	if args[0] == "estimate" {
		var input installplan.CostInput
		input, err = installplan.DecodeCost(in)
		if err == nil {
			result, err = installplan.Estimate(input)
		}
	} else if args[0] == "report" {
		var input installplan.ReportInput
		input, err = installplan.DecodeReport(in)
		if err == nil {
			result, err = installplan.BuildReport(input)
		}
	} else {
		var input installplan.Input
		input, err = installplan.Decode(in)
		if err == nil {
			if args[0] == "doctor-clock" {
				var report clockcheck.Report
				report, err = collect(context.Background(), input)
				result = report
				if !report.DiagnosticOK() {
					exitCode = 3
				}
			} else {
				result, err = installplan.Build(input)
			}
		}
	}
	if err != nil {
		fmt.Fprintln(stderr, installplan.ErrInput)
		return 2
	}
	b, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fmt.Fprintln(stderr, "plan encoding failed")
		return 1
	}
	if _, err = out.Write(append(b, '\n')); err != nil {
		fmt.Fprintln(stderr, "plan output failed")
		return 1
	}
	return exitCode
}
