# NudgeOn Waiting Room Preview

Requires Docker Engine with Docker Compose on Linux, or Docker Desktop on macOS.
This is a loopback-only local Preview with a demo origin. It is not a production deployment.

Extract the archive for your OS and CPU, then run:

```sh
./wrctl install
./wrctl setup
```

`install` downloads the GHCR runtime pinned into this binary, prepares private
configuration and persistent Docker volumes, and starts the services. No Go,
Node.js, repository clone, or source build is required. TOTP is on by default.

`setup` prints a private, 15-minute one-time token and opens the local setup tunnel
at `https://127.0.0.1:19444/setup`. Complete first-admin and authenticator registration,
save the recovery codes, then close the tunnel with Ctrl-C. Use the admin console
at `https://127.0.0.1:19443` and the demo Gateway at `https://127.0.0.1:20443`.
Certificates are local and self-signed.

```sh
./wrctl status
./wrctl stop
./wrctl up
```

To upgrade, download and extract the next release, back up your installation state
and volumes, then run its `./wrctl upgrade`. Upgrade pulls and checks the new image
before stopping services, preserves existing state, and restarts the installation.
It involves downtime. On a migration failure, retry the same upgrade; automatic
rollback or old queue-schema conversion is not supported. Do not delete volumes
or regenerate keys to recover an interrupted upgrade.

Use `--directory /absolute/private/path` consistently to select another installation.
The default is the platform's user configuration directory under `waiting-room`.
Docker ports are fixed at loopback 19443/19444/20443, so run one local Preview at a time.

The archive's `runtime-image.txt` contains the exact OCI digest. `SHA256SUMS` on the
release page verifies archive bytes; a checksum is not a signature. macOS binaries
are unsigned and not notarized.

Documentation: https://github.com/NudgeOn/Waiting-Room/blob/main/docs/local-docker.md
License: Apache-2.0
