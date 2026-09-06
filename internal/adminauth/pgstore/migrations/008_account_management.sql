-- SPDX-License-Identifier: Apache-2.0
-- IDs remain reserved after deletion so durable actor references cannot be
-- reassigned. Credentials, challenges and sessions are removed separately.
ALTER TABLE auth_accounts ADD COLUMN deleted boolean NOT NULL DEFAULT false;
ALTER TABLE auth_accounts ADD CONSTRAINT deleted_account_disabled CHECK (NOT deleted OR NOT enabled);

CREATE FUNCTION lock_account_authority() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM singleton FROM auth_policy WHERE singleton FOR UPDATE;
    RETURN NULL;
END $$;
CREATE TRIGGER account_authority_lock BEFORE UPDATE OR DELETE ON auth_accounts
    FOR EACH STATEMENT EXECUTE FUNCTION lock_account_authority();

CREATE FUNCTION preserve_last_admin() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (SELECT 1 FROM auth_bootstrap WHERE singleton AND completed)
       AND NOT EXISTS (SELECT 1 FROM auth_accounts WHERE role='admin' AND enabled AND NOT deleted) THEN
        RAISE EXCEPTION 'LAST_ADMIN_REQUIRED' USING ERRCODE='23514';
    END IF;
    RETURN NULL;
END $$;
CREATE TRIGGER account_last_admin AFTER UPDATE OR DELETE ON auth_accounts
    FOR EACH STATEMENT EXECUTE FUNCTION preserve_last_admin();
