# M0 threat model

Scope: contracts and oracle. Controls marked planned need runtime evidence before delivery GO.

| ID | Asset / attack | Required control | Verification | State |
|---|---|---|---|---|
| TH-01 | Queue order / overtaking | atomic primary sequence and FIFO eligibility | TestFIFOAndCapacity; Valkey equivalence at M1 | MODEL PASS; runtime pending |
| TH-02 | Origin capacity / duplicate claims | READY+lease atomic budget; fixed JTI/expiry; skew retention | TestClaimRetryAndSkewReservation | MODEL PASS; runtime pending |
| TH-03 | Origin / rate bursts | rolling half-open 60-second window | TestReadyExpiryDoesNotRefundRate; randomized oracle | MODEL PASS; runtime pending |
| TH-04 | Queue / memory exhaustion | visitor+idempotency budgets, source quota, noeviction | TestJoinReplayAndBudget; production churn at M4 | PARTIAL |
| TH-05 | Origin / uncertain failover | fenced unsafeUntil; revoke requires Gateway ack/fencing | TestRecoveryWindowAndNewEpoch; chaos at M5 | PARTIAL |
| TH-06 | Origin / route bypass, spoofed headers | normalize once, reject ambiguity, strip internal headers | UT-03-07/12, fuzz at M2 | PLANNED |
| TH-07 | Browser / open redirect | AEAD host/room/expiry binding; relative paths | UT-03-11 | PLANNED |
| TH-08 | Admin / CSRF, MFA bypass | Origin+CSRF, scoped challenges, all-role session revoke | UT-04-03/05/07 | PLANNED |
| TH-09 | Config / rollback or tamper | pinned key, monotonic generation, atomic LKG swap | UT-05-04/05 | PLANNED |
| TH-10 | Internal service / impersonation | isolated credentials, mTLS and role scope | UT-05-01/03 | PLANNED |
| TH-11 | Host / SSRF and DNS rebinding | address allowlist checked on every resolve/connect | UT-05-08 | PLANNED |
| TH-12 | Secrets / logs and backups | redaction, external wrapping key, restore digest checks | UT-05-09/11 | PLANNED |
| TH-13 | Bearer token theft / sharing | TLS, HttpOnly, short fixed lifetime | explicitly accepted v1 residual risk | NON-GOAL |
| TH-14 | Last Admin removal | transactional last-admin invariant, CLI recovery | UT-04-09 | PLANNED |

Runtime tests must prove origin reach is limited to Gateway-protected paths; an unverified
origin firewall cannot be advertised as verified bypass protection. Security reporting
contact and patch scan remain publication gates.
