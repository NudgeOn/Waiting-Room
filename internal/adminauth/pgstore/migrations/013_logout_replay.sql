-- SPDX-License-Identifier: Apache-2.0
-- A revoked session can only recover its previous logout acknowledgement.
-- No session, credential, raw CSRF or raw idempotency key is retained here.
CREATE TABLE auth_logout_replays (
    session_hash bytea PRIMARY KEY CHECK(octet_length(session_hash)=32),
    csrf_hash bytea NOT NULL CHECK(octet_length(csrf_hash)=32),
    key_hash bytea NOT NULL CHECK(octet_length(key_hash)=32),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    expires_at timestamptz NOT NULL DEFAULT clock_timestamp()+interval '24 hours',
    CHECK(expires_at>created_at)
);
CREATE INDEX auth_logout_expiry ON auth_logout_replays(expires_at);
