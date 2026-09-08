-- SPDX-License-Identifier: Apache-2.0
-- Existing credentials retain their original wrp1 / t=2 parameters. Setup may
-- configure a new installation only before any administrator is created.
CREATE TABLE installation_setup (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    password_iterations integer NOT NULL DEFAULT 2 CHECK (password_iterations BETWEEN 2 AND 10),
    calibration jsonb,
    calibration_digest text,
    calibration_generation bigint,
    calibrated_at timestamptz,
    report jsonb,
    applied_at timestamptz,
    CHECK ((report IS NULL) = (applied_at IS NULL)),
    CHECK (calibration_digest IS NULL OR calibration_digest ~ '^[a-f0-9]{64}$')
);
INSERT INTO installation_setup(singleton) VALUES(true);
