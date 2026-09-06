# ADR-0002 — Read-only status, reservation lifetime and recovery

Status: accepted; refines SUB-PRD-02/03 before runtime implementation.

1. GET status is read-only, including idle expiry. Waiting clients explicitly POST heartbeat
   every five minutes. Heartbeat updates idle expiry only, capped by absolute expiry.
   A heartbeat cannot promote, claim or revive an expired ticket.
2. READY creates the fixed JTI and expiry: `admissionUntil = promotedAt + admissionTTL`.
   `readyUntil = min(promotedAt + readyTTL, admissionUntil)`. Late claim receives only the
   remaining token lifetime. Signing retries never extend it.
3. Capacity counts READY plus ADMITTED. An ADMITTED lease remains reserved through
   `admissionUntil + maximum verifier expiry leeway` (30 seconds), preventing oversubscription
   while old tokens are still accepted.
4. Rate uses the half-open interval `(now - 60s, now]`; READY consumes rate, abandonment
   does not refund it. An expired READY has never issued a token and releases its reservation.
5. Recovery starts from an acknowledged fenced primary handshake, using its clock:
   `unsafeUntil = handshakeAt + max(admissionTTL, readyTTL, 60s) + 30s`.
   Repeated uncertainty can only extend the hold.
6. Emergency new-epoch recovery is NOT an immediate bypass around this wait unless every
   serving Gateway acknowledges the new epoch and stale/unreachable Gateways are removed
   from the traffic path. Until that is proven, hold remains enforced. M1 must test this.
7. The oracle retains expired tickets for inspection and models ID idempotency, not encrypted
   byte-for-byte HTTP response replay. Production storage must separately enforce memory bounds.
8. Default 10-minute idempotency retention cannot sustain 1,000 fresh joins/s with a 200K
   record budget. Qualification separates population, sustained flow and saturation phases;
   steady flow is 30/300 fresh joins and claims per second for Standard/High, with the default
   600-second idempotency retention (18K/180K records). Peak 100/1,000 claims/s is a separate
   fresh 60-second phase. Admission TTL is an operator-configurable 60 seconds for these tests;
   the default 15-minute TTL is separately tested for safe saturation, not identical throughput.
   No test-only retention override is allowed. Population, flow and peak are distinct claims.
