-- SPDX-License-Identifier: Apache-2.0
-- Explicit migration only. Apply after 001-004 using the migration role.
CREATE TABLE control_config (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    revision bigint NOT NULL CHECK (revision >= 0 AND revision < 9007199254740990),
    document jsonb NOT NULL CHECK (jsonb_typeof(document) = 'object'),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
-- Session hash and raw request/key are intentionally not retained.
CREATE TABLE control_commands (
    actor_id text NOT NULL REFERENCES auth_accounts(id),
    key_hash bytea NOT NULL CHECK (octet_length(key_hash) = 32),
    request_hash bytea NOT NULL CHECK (octet_length(request_hash) = 32),
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    status integer NOT NULL CHECK (status BETWEEN 200 AND 599),
    response bytea NOT NULL CHECK (octet_length(response) BETWEEN 1 AND 65536),
    etag text NOT NULL,
    PRIMARY KEY (actor_id, key_hash),
    CHECK (expires_at = created_at + interval '24 hours')
);
CREATE INDEX control_commands_expiry ON control_commands(expires_at);
CREATE TABLE control_audit (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    actor_id text NOT NULL,
    actor_role text NOT NULL CHECK (actor_role IN ('admin','operator','viewer')),
    action text NOT NULL,
    target_id text NOT NULL,
    before_digest text NOT NULL CHECK (before_digest ~ '^[a-f0-9]{64}$'),
    after_digest text NOT NULL CHECK (after_digest ~ '^[a-f0-9]{64}$'),
    result text NOT NULL,
    request_id text NOT NULL,
    revision bigint NOT NULL CHECK (revision >= 0)
);
