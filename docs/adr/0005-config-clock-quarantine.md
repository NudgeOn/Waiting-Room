# ADR-0005 — Recover signed configuration after a wall-clock correction

Status: accepted for the local Beta runtime. Refines the configuration clock
latch only; queue recovery fences and admission safety windows are unchanged.

The 2026-09-09 incident has a measured host wall-clock adjustment of -2.002746
seconds at the time four independent runtime nodes stopped accepting trusted
configuration. The original diagnostic collapsed the first cause into
`snapshot_unavailable`. [Clock evidence](../evidence/beta-20260910-clock-recovery/incident-host-clock.json)
records the observed host change separately from the inferred node trigger.
The previous Gate permanently rejected even fresh signed configuration after
any observed backward clock step. Waiting for the queue's epoch safety deadline
could not repair that independent configuration latch.

1. An observed rollback still closes every protected request and new background
   operation. Record the greatest observed wall-clock second as a recovery
   boundary. Keep the original signed snapshot and durable generation/revision.
2. Clock catch-up, `Current()`, an exact snapshot replay, an invalid publication,
   and a newer generation issued before the recovery boundary cannot reopen it.
3. Recovery requires the clock to reach its previous high-water mark, a complete
   valid signed publication with a strictly newer generation, and `issuedAt` at
   or after the recovery boundary. All normal installation/key/schema/revision/
   generation/issuedAt/expiry checks still apply, without time leeway.
4. Persist the accepted publication through the normal atomic write/fsync path
   before clearing quarantine. A persistence failure remains a permanent latch;
   corrupt restoration is never repaired by receiving a fresh publication.
5. A second rollback advances the recovery boundary. Old/expired snapshots are
   never resurrected. This is a new authorized publication, not an extension of
   the old snapshot's lifetime or an automatic restart/reset of trust storage.
6. The normal Control refresh runs every five minutes from the approved
   configuration, excluding draft changes. A quarantined data node additionally
   requests `POST /internal/v1/config/clock-recovery` through its verified mTLS
   identity. The bounded request names its accepted generation/digest and clock
   boundary. Control waits for its own clock to reach that boundary and signs
   the current approved delivery under the normal transaction lock. It never
   takes configuration, a mode, an epoch or a clock value from the caller.
   Concurrent requests reuse an already eligible generation; the signature and
   one `config.clock_recovery` audit event commit atomically. A newer operator
   publication made before the boundary is re-signed without undoing its state.
   The node still verifies and durably stores the full new envelope before
   reopening. Availability remains conditional on the queue's separate recovery
   state and both runtime ACKs. Unavailable Control or storage stays closed.
7. This does not alter host NTP settings, public quotas, token TTLs, Valkey clock
   handling, `unsafeUntil`, installation epochs or runtime modes. A recovered
   queue returns to HOLD and still requires an explicit AUTO command.

Process restart, disk rollback by an owner, and VM snapshot rollback remain the
pre-existing deployment trust limitations. This change does not claim to solve
them. Fake clock injection is confined to deterministic unit tests; the Docker
population and full epoch tests use real clocks and production policy values.

The 2026-09-10 06:43:25 UTC run directly measured a -2.016493222 second
wall-clock step inside the same Docker VM, while 9.7525ms elapsed monotonically.
Both independent six-role fixtures recorded `snapshot_clock_rollback` at that
time. Their tests stopped before the five-minute renewal; this proves the new
trigger and the missing prompt recovery path, not an indefinite latch in that
candidate. The earlier historical incidents remain separate observations.
