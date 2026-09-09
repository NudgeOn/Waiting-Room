// SPDX-License-Identifier: Apache-2.0
// Package installer manages one explicitly requested local Docker preview.
package installer

import (
	"errors"
	"path/filepath"
	"regexp"
)

// DefaultImage is set by the release build only after publishing the selected
// runtime digest. A source build has no implicit image or floating-tag fallback.
var DefaultImage string
var Version = "source"

const ImageRepository = "ghcr.io/nudgeon/waiting-room"
const ImageSource = "https://github.com/NudgeOn/Waiting-Room"
const Usage = "wrctl keys-stage|keys-activate|keys-retire|keys-revoke|keys-status [--directory PATH]\nwrctl backup|restore --backup-directory PATH [--directory PATH]\nwrctl install [--image ghcr.io/nudgeon/waiting-room@sha256:DIGEST] [--totp on|off] [--directory PATH]\nwrctl up|stop|setup|bootstrap|token [--directory PATH]\nwrctl status [--json] [--directory PATH]\nwrctl upgrade [--image ghcr.io/nudgeon/waiting-room@sha256:DIGEST] [--directory PATH]"

var imagePattern = regexp.MustCompile(`^ghcr\.io/nudgeon/waiting-room@sha256:[a-f0-9]{64}$`)
var projectPattern = regexp.MustCompile(`^waiting-room-preview-[a-f0-9]{16}$`)
var revisionPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)

type options struct {
	command, directory, image, totp string
	backupDirectory                 string
	totpExplicit                    bool
	json                            bool
}

func Handles(command string) bool {
	switch command {
	case "keys-stage", "keys-activate", "keys-retire", "keys-revoke", "keys-status", "install", "up", "upgrade", "status", "stop", "setup", "bootstrap", "token", "backup", "restore":
		return true
	}
	return false
}

func parse(args []string, directory string) (options, error) {
	if len(args) == 0 || !Handles(args[0]) {
		return options{}, errors.New("invalid runtime command")
	}
	o := options{command: args[0], directory: directory, totp: "on"}
	seen := map[string]bool{}
	for n := 1; n < len(args); {
		if seen[args[n]] {
			return options{}, errors.New("invalid runtime options; use --option value once")
		}
		seen[args[n]] = true
		if args[n] == "--json" && o.command == "status" {
			o.json = true
			n++
			continue
		}
		if n+1 >= len(args) {
			return options{}, errors.New("invalid runtime options; use --option value once")
		}
		switch args[n] {
		case "--backup-directory":
			if o.command != "backup" && o.command != "restore" || args[n+1] == "" {
				return options{}, errors.New("backup-directory is required for backup and restore")
			}
			var err error
			o.backupDirectory, err = filepath.Abs(args[n+1])
			if err != nil {
				return options{}, errors.New("invalid backup directory")
			}
		case "--directory":
			if args[n+1] == "" {
				return options{}, errors.New("installation directory is required")
			}
			o.directory = args[n+1]
		case "--image":
			if o.command != "install" && o.command != "upgrade" {
				return options{}, errors.New("image selection is only supported by install and upgrade")
			}
			o.image = args[n+1]
			if !imagePattern.MatchString(o.image) {
				return options{}, errors.New("image must use the official GHCR repository and an exact lowercase sha256 digest")
			}
		case "--totp":
			if o.command != "install" || (args[n+1] != "on" && args[n+1] != "off") {
				return options{}, errors.New("initial TOTP must be on or off and is only selected during install")
			}
			o.totp, o.totpExplicit = args[n+1], true
		default:
			return options{}, errors.New("unsupported runtime option")
		}
		n += 2
	}
	if o.directory == "" {
		return options{}, errors.New("installation directory unavailable; use --directory")
	}
	if (o.command == "backup" || o.command == "restore") && o.backupDirectory == "" {
		return options{}, errors.New("use --backup-directory PATH")
	}
	abs, err := filepath.Abs(o.directory)
	if err != nil {
		return options{}, errors.New("installation directory unavailable")
	}
	o.directory = filepath.Clean(abs)
	return o, nil
}
