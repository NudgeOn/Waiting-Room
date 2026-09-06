# Failure matrix

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
