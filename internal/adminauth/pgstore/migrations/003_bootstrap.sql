-- SPDX-License-Identifier: Apache-2.0
-- One-time migration, using a migration role with the application stopped.
CREATE TABLE auth_bootstrap (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    completed boolean NOT NULL DEFAULT false,
    generation bigint NOT NULL DEFAULT 0 CHECK (generation >= 0),
    token_hash bytea CHECK (octet_length(token_hash) = 32),
    token_expires_at timestamptz,
    CHECK ((token_hash IS NULL) = (token_expires_at IS NULL)),
    CHECK (NOT completed OR token_hash IS NULL)
);
-- Existing installations must never acquire a new first-admin path.
INSERT INTO auth_bootstrap (completed) SELECT EXISTS(SELECT 1 FROM auth_accounts);

-- Restricted proof of password authentication, NOT a session or TOTP login token.
-- Secret provisioning/verification/recovery generation are a subsequent slice.
CREATE TABLE auth_enrollment_challenges (
    token_hash bytea PRIMARY KEY CHECK (octet_length(token_hash) = 32),
    user_id text NOT NULL REFERENCES auth_accounts(id),
    policy_version bigint NOT NULL CHECK (policy_version > 0),
    user_version bigint NOT NULL CHECK (user_version > 0),
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    consumed boolean NOT NULL DEFAULT false,
    CHECK (expires_at > created_at AND expires_at <= created_at + interval '5 minutes')
);
CREATE INDEX auth_enrollment_user ON auth_enrollment_challenges(user_id);
CREATE INDEX auth_enrollment_expiry ON auth_enrollment_challenges(expires_at);
