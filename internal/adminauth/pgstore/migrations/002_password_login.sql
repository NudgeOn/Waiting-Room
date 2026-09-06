-- SPDX-License-Identifier: Apache-2.0
-- Apply after 001 using the migration role, in one transaction.
CREATE TABLE auth_password_credentials (
    user_id text PRIMARY KEY REFERENCES auth_accounts(id),
    version bigint NOT NULL CHECK (version > 0),
    password_hash text NOT NULL CHECK (length(password_hash) BETWEEN 1 AND 160)
);
-- Only HMAC identifiers, never raw user input/IP. Global-first reservation limits
-- live row cardinality; expired rows are pruned on the next allowed reservation.
CREATE TABLE auth_login_buckets (
    bucket_key text PRIMARY KEY CHECK (length(bucket_key) BETWEEN 1 AND 80),
    started_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL CHECK (expires_at > started_at),
    used integer NOT NULL CHECK (used > 0)
);
CREATE INDEX auth_login_buckets_expiry ON auth_login_buckets(expires_at);
