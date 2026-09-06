# ADR-0001 — M0 contracts and oracle boundaries

Status: accepted for M0 implementation.

- The local Go module is `waiting-room`; a published import namespace is deferred until a remote is chosen.
- M0 ships contracts, a single-threaded Go queue reference model, test tooling and governance.
- No network server, shared store, installer, user authentication or production service is claimed.
- M0 GO authorizes M1 work; it never changes sub-PRD delivery NO-GO or MAIN release NO-GO.
- Read dependencies on MAIN mean product context, not a circular delivery dependency.
- Unit/property tests on the oracle do not prove Valkey atomicity, multi-replica safety or throughput.
- Apache-2.0 source and external dev tools are inventoried separately. No UI framework is selected yet.
