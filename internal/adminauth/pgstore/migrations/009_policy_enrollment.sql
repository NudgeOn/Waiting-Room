-- SPDX-License-Identifier: Apache-2.0
-- This marker is created ONLY by an authenticated, action-bound Admin command.
-- Ordinary login enrollment remains unavailable while the global policy is OFF.
CREATE TABLE auth_policy_enrollments (
    token_hash bytea PRIMARY KEY REFERENCES auth_enrollment_challenges(token_hash) ON DELETE CASCADE,
    session_hash bytea NOT NULL REFERENCES auth_sessions(token_hash) ON DELETE CASCADE
);
CREATE INDEX auth_policy_enrollments_session ON auth_policy_enrollments(session_hash);
