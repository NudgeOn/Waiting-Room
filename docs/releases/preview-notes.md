First downloadable Preview of NudgeOn Waiting Room: a self-hosted virtual waiting room for websites and apps.

## Try it

Download the `wrctl` archive for Linux or macOS and your CPU (`amd64` or `arm64`), verify it against `SHA256SUMS`, and extract it. Docker with Compose must be running.

```sh
./wrctl install
./wrctl setup
```

No Go, Node.js, or source checkout is required. The binary pins the prebuilt GHCR runtime by digest; installation checks its platform and source metadata. TOTP is enabled by default. Complete the first-admin wizard using the private token shown by `setup`, then open `https://127.0.0.1:19443`.

Use `wrctl stop` / `wrctl up` to stop and restart. After downloading a newer CLI, `wrctl upgrade` pulls its pinned runtime before stopping services and preserves the installation's volumes, keys, accounts and Room settings. Migration failure blocks ordinary startup until the upgrade is retried.

## Operate

- Dashboard and Room operations show WAITING, READY, ADMITTED, the observed admission count over 60 seconds, origin health and recent Gateway HTTP 5xx errors.
- HOLD and AUTO controls use the existing authenticated runtime command path. Missing or stale observations are identified and actions are gated on current state.
- Five-step Room creation provides inline address/path/capacity validation, editable review, and live waiting-page text, color and language preview.
- Six-step installation planning separates scale, environment, traffic and security, with deterministic plan downloads and optional user-priced cost comparison.

## Preview boundaries

This release is for isolated local evaluation. It binds to loopback, uses local self-signed TLS and includes a demo origin. It is not a public production installer, a Kubernetes/HA release, or 10K/100K performance qualification. Preview upgrades can require downtime; old runtime queue-schema migration, production recovery acceptance, and full Traffic Lab UI remain incomplete. macOS CLI binaries are unsigned and not notarized.

HTTP error counts cover the current Gateway's final Room-attributed 5xx responses over a rolling minute; an incomplete observation window is identified. ADMITTED is active admission state, not an online-user counter. Image labels and archive checksums establish identity/integrity checks, not performance or HA certification.

See the [local Docker guide](https://github.com/NudgeOn/Waiting-Room/blob/main/docs/local-docker.md), [release quick start](https://github.com/NudgeOn/Waiting-Room/blob/main/docs/releases/quick-start.md), and [Beta checklist](https://github.com/NudgeOn/Waiting-Room/blob/main/docs/beta-plan.md).
