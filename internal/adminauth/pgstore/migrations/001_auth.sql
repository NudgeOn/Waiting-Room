-- SPDX-License-Identifier: Apache-2.0
-- Apply once using a dedicated migration role, never the HTTP runtime role.
-- Minimal TOTP-login storage slice; bootstrap/password/recovery flows are pending.
CREATE TABLE auth_policy (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    mode text NOT NULL CHECK (mode IN ('configurable', 'forced_on')),
    totp_enabled boolean NOT NULL,
    version bigint NOT NULL CHECK (version > 0),
    CHECK (mode <> 'forced_on' OR totp_enabled)
);
INSERT INTO auth_policy VALUES (true, 'configurable', true, 1);

CREATE TABLE auth_accounts (
    id text PRIMARY KEY CHECK (id ~ '^[A-Za-z0-9_-]{1,128}$'),
    role text NOT NULL CHECK (role IN ('admin', 'operator', 'viewer')),
    enabled boolean NOT NULL DEFAULT true,
    session_version bigint NOT NULL DEFAULT 1 CHECK (session_version > 0)
);
CREATE TABLE auth_totp_credentials (
    user_id text PRIMARY KEY REFERENCES auth_accounts(id),
    version bigint NOT NULL CHECK (version > 0),
    key_id text NOT NULL CHECK (length(key_id) BETWEEN 1 AND 64),
    sealed_secret bytea NOT NULL CHECK (octet_length(sealed_secret) = 60),
    last_counter bigint NOT NULL DEFAULT -1 CHECK (last_counter >= -1)
);
-- Insertion is restricted to a future trusted, throttled password-login flow.
-- These are NOT caller assertions of password verification.
CREATE TABLE auth_totp_challenges (
    token_hash bytea PRIMARY KEY CHECK (octet_length(token_hash) = 32),
    user_id text NOT NULL REFERENCES auth_accounts(id),
    credential_version bigint NOT NULL CHECK (credential_version > 0),
    policy_version bigint NOT NULL CHECK (policy_version > 0),
    user_version bigint NOT NULL CHECK (user_version > 0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    expires_at timestamptz NOT NULL,
    attempts smallint NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 5),
    consumed boolean NOT NULL DEFAULT false,
    CHECK (expires_at > created_at AND expires_at <= created_at + interval '5 minutes')
);
CREATE INDEX auth_challenges_user ON auth_totp_challenges(user_id);
CREATE INDEX auth_challenges_expiry ON auth_totp_challenges(expires_at);
CREATE TABLE auth_sessions (
    token_hash bytea PRIMARY KEY CHECK (octet_length(token_hash) = 32),
    csrf_hash bytea NOT NULL CHECK (octet_length(csrf_hash) = 32),
    user_id text NOT NULL REFERENCES auth_accounts(id),
    policy_version bigint NOT NULL CHECK (policy_version > 0),
    user_version bigint NOT NULL CHECK (user_version > 0),
    mfa_verified boolean NOT NULL,
    created_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL CHECK (last_seen_at >= created_at)
);
CREATE INDEX auth_sessions_user ON auth_sessions(user_id);
CREATE INDEX auth_sessions_created ON auth_sessions(created_at);
