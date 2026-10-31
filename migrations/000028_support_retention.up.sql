-- Retention: cancellations and refunds go to the support managers' queue, and the bot
-- may give free days of a paid plan (the only billing change it makes itself).
ALTER TABLE support_tickets DROP CONSTRAINT support_tickets_queue_check,
    ADD CONSTRAINT support_tickets_queue_check CHECK (queue IN ('moderators', 'admins', 'support'));

ALTER TABLE support_actions DROP CONSTRAINT support_actions_type_check,
    ADD CONSTRAINT support_actions_type_check CHECK (type IN ('restore_idea', 'notify_staff', 'bug_report', 'data_request', 'extend_plan'));

-- One gift of free days per person, found by the fantasm handle the action was made for.
-- 'approved' counts too: a colleague's approval applies the gift before it is recorded.
CREATE INDEX support_actions_goodwill_idx ON support_actions ((params ->> 'handle'))
    WHERE type = 'extend_plan' AND status IN ('executed', 'approved');
