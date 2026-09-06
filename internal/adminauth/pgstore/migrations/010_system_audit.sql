-- SPDX-License-Identifier: Apache-2.0
-- Service observations are not human administrator actions. This does NOT add
-- a login/account role or grant system actors any HTTP capabilities.
ALTER TABLE control_audit DROP CONSTRAINT control_audit_actor_role_check;
ALTER TABLE control_audit ADD CONSTRAINT control_audit_actor_role_check
    CHECK (actor_role IN ('admin','operator','viewer','system'));
