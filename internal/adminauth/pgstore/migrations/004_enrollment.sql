-- SPDX-License-Identifier: Apache-2.0
ALTER TABLE auth_enrollment_challenges
    ADD COLUMN attempts smallint NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 5),
    ADD COLUMN key_id text CHECK (key_id ~ '^[A-Za-z0-9_-]{1,64}$'),
    ADD COLUMN sealed_secret bytea CHECK (octet_length(sealed_secret) = 60),
    ADD CONSTRAINT enrollment_secret_pair CHECK ((key_id IS NULL) = (sealed_secret IS NULL));

ALTER TABLE auth_totp_credentials ADD CONSTRAINT totp_user_version UNIQUE (user_id,version);
CREATE TABLE auth_recovery_codes (
    user_id text NOT NULL,
    credential_version bigint NOT NULL CHECK (credential_version > 0),
    slot smallint NOT NULL CHECK (slot BETWEEN 1 AND 10),
    code_hash text NOT NULL CHECK (length(code_hash) BETWEEN 1 AND 160),
    consumed boolean NOT NULL DEFAULT false,
    PRIMARY KEY (user_id,credential_version,slot),
    FOREIGN KEY (user_id,credential_version) REFERENCES auth_totp_credentials(user_id,version)
);
-- The leading PK columns cover the composite foreign key.
