-- SPDX-License-Identifier: Apache-2.0
CREATE TABLE auth_reauth_proofs (
 token_hash bytea PRIMARY KEY CHECK(octet_length(token_hash)=32),
 session_hash bytea NOT NULL REFERENCES auth_sessions(token_hash) ON DELETE CASCADE,
 action text NOT NULL,
 target_id text NOT NULL CHECK(length(target_id) BETWEEN 1 AND 128),
 request_digest text NOT NULL CHECK(request_digest ~ '^[a-f0-9]{64}$'),
 created_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL CHECK(expires_at>created_at AND expires_at<=created_at+interval '5 minutes'),
 consumed boolean NOT NULL DEFAULT false
);
CREATE INDEX auth_reauth_session ON auth_reauth_proofs(session_hash);
CREATE INDEX auth_reauth_expiry ON auth_reauth_proofs(expires_at);
