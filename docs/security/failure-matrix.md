# Failure matrix

## Local Docker observations

These results describe the local M1–M3 candidate, not HA or production qualification.
[Read recovery and guard upgrade evidence](../evidence/beta-20260909-read-recovery.md)
records both failed-before and passed-after executions on actual Valkey connections.

| Event | New requests | Recovery | Verified boundary |
|---|---|---|---|
| Read-only reply lost (`INFO` / `FCALL_RO`) | Failed request receives 503; subsequent verified requests can retry | Same client reconnects; no uncertain-write hold is created | Legacy/v6 socket tests; v6 Gateway/Coordinator HTTP retry, exact join response, FIFO and claim |
| Write reply lost | Admission remains blocked | Shared fence and original unsafeUntil; no shortened safety wait | Actual v6 lost-write response and legacy failure latch |
| Server invariant error during read | Admission remains blocked | Existing invariant/recovery validation | Wrong-type fault in isolated legacy/v6 ticket data |
| Existing public guard v1 differs from the tracked v1 body | Old library is retained; new Coordinator requires exact v2 | Owner installs immutable v2 during upgrade with application roles stopped | Two installs preserve old library bytes, all three keys and the existing poll deadline |

The historical 5K failure and Admin epoch ACK delay lack the original state needed
to establish their exact causes. The new regressions do not retrospectively prove
those causes. [Beta acceptance](../beta-plan.md) remains separate.

## Deployment qualification matrix

| Event | Existing admitted tokens | New admissions | Resume requirement | Evidence |
|---|---|---|---|---|
| Control unavailable, valid LKG | continue until expiry | last valid queue config | reconnect with newer verified generation | M2 pending |
| No LKG or expired snapshot | liveness only, all other requests 503 | stopped | valid signed snapshot | M2 pending |
| Valkey primary uncertainty | pass within signed token bounds | stopped | fenced handshake + unsafeUntil + invariant checks | model test; M5 pending |
| Coordinator lost | other authenticated Coordinator handles | budget still atomic | readiness / retry | M1 pending |
| Idempotency budget full | continue | existing ticket claim allowed; new keys rejected | record expiry frees budget | model test; M4 pending |
| Valkey noeviction write error | continue | stopped | memory/invariant recovery | M4 pending |
| Emergency epoch | old tokens invalid only after verifier rollout | stopped until stale Gateways fenced | all active Gateway acknowledgments | M2/M5 pending |
| Origin failure | operator-controlled HOLD | no automatic unsafe bypass | origin health and operator decision | M3 pending |
