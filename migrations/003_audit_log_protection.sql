-- 003_audit_log_protection.sql

-- Prevent updates to audit_log (append-only)
CREATE OR REPLACE RULE no_update_audit AS 
    ON UPDATE TO audit_log 
    DO INSTEAD NOTHING;

-- Prevent deletes from audit_log (append-only)
CREATE OR REPLACE RULE no_delete_audit AS 
    ON DELETE FROM audit_log 
    DO INSTEAD NOTHING;
