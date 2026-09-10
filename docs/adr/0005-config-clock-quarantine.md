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
6. The existing Control refresh runs every five minutes from the approved
   configuration, excluding draft changes. Once time and dependencies are
   healthy, a fresh refresh can restore configuration without deleting volumes
   or restarting every node. Availability is still conditional on the queue's
   separate recovery state and both runtime ACKs.
7. This does not alter host NTP settings, public quotas, token TTLs, Valkey clock
   handling, `unsafeUntil`, installation epochs or runtime modes. A recovered
   queue returns to HOLD and still requires an explicit AUTO command.

Process restart, disk rollback by an owner, and VM snapshot rollback remain the
pre-existing deployment trust limitations. This change does not claim to solve
them. Fake clock injection is confined to deterministic unit tests; the Docker
population and full epoch tests use real clocks and production policy values.
