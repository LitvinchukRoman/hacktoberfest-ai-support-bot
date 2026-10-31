DROP INDEX IF EXISTS support_actions_goodwill_idx;

DELETE FROM support_actions WHERE type = 'extend_plan';
ALTER TABLE support_actions DROP CONSTRAINT support_actions_type_check,
    ADD CONSTRAINT support_actions_type_check CHECK (type IN ('restore_idea', 'notify_staff', 'bug_report', 'data_request'));

UPDATE support_tickets SET queue = 'admins' WHERE queue = 'support';
ALTER TABLE support_tickets DROP CONSTRAINT support_tickets_queue_check,
    ADD CONSTRAINT support_tickets_queue_check CHECK (queue IN ('moderators', 'admins'));
