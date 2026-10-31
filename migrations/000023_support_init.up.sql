CREATE TABLE support_tickets (
    id                uuid        PRIMARY KEY,
    user_id           uuid        REFERENCES users (id) ON DELETE CASCADE,
    anon_key_hash     bytea,
    contact_email     text        CHECK (contact_email IS NULL OR char_length(contact_email) <= 254),
    status            text        NOT NULL DEFAULT 'processing'
                                  CHECK (status IN ('processing', 'awaiting_customer', 'awaiting_approval', 'escalated', 'solved')),
    autonomy          text        CHECK (autonomy IN ('auto', 'approve', 'escalate')),
    intent            text        NOT NULL DEFAULT '',
    emotion           text        NOT NULL DEFAULT '',
    language          text        NOT NULL DEFAULT 'uk',
    priority          smallint    NOT NULL DEFAULT 3 CHECK (priority BETWEEN 1 AND 3),
    queue             text        CHECK (queue IN ('moderators', 'admins')),
    escalation_reason text,
    idea_id           uuid        REFERENCES ideas (id) ON DELETE SET NULL,
    page              text        NOT NULL DEFAULT '' CHECK (char_length(page) <= 300),
    flow              text        NOT NULL DEFAULT '',
    flow_step         smallint    NOT NULL DEFAULT 0,
    last_seq          bigint      NOT NULL DEFAULT 0,
    first_reply_at    timestamptz,
    solved_at         timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT support_tickets_owner CHECK (user_id IS NOT NULL OR anon_key_hash IS NOT NULL)
);
CREATE INDEX support_tickets_inbox_idx ON support_tickets (priority, updated_at) WHERE status IN ('awaiting_approval', 'escalated');
CREATE INDEX support_tickets_user_idx ON support_tickets (user_id, created_at DESC) WHERE user_id IS NOT NULL;
CREATE INDEX support_tickets_anon_idx ON support_tickets (anon_key_hash) WHERE anon_key_hash IS NOT NULL;
CREATE INDEX support_tickets_idea_idx ON support_tickets (idea_id) WHERE idea_id IS NOT NULL;
CREATE INDEX support_tickets_created_idx ON support_tickets (created_at);

-- Тексти зберігаються ПІСЛЯ redaction.
CREATE TABLE support_messages (
    id           uuid        PRIMARY KEY,
    ticket_id    uuid        NOT NULL REFERENCES support_tickets (id) ON DELETE CASCADE,
    author       text        NOT NULL CHECK (author IN ('customer', 'bot', 'agent')),
    agent_id     uuid        REFERENCES users (id) ON DELETE SET NULL,
    visibility   text        NOT NULL DEFAULT 'public' CHECK (visibility IN ('public', 'internal')),
    body         text        NOT NULL CHECK (char_length(body) BETWEEN 1 AND 4000),
    actions      jsonb       NOT NULL DEFAULT '[]',
    pii_found    text[]      NOT NULL DEFAULT '{}',
    processed_at timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT support_messages_internal CHECK (visibility = 'public' OR author = 'agent')
);
CREATE INDEX support_messages_ticket_idx ON support_messages (ticket_id, created_at);
CREATE INDEX support_messages_unprocessed_idx ON support_messages (ticket_id, created_at) WHERE author = 'customer' AND processed_at IS NULL;
CREATE INDEX support_messages_agent_idx ON support_messages (agent_id) WHERE agent_id IS NOT NULL;

-- staff — повний JSON події; public — для клієнта (trace без data), NULL = лише персонал.
CREATE TABLE support_events (
    ticket_id  uuid        NOT NULL REFERENCES support_tickets (id) ON DELETE CASCADE,
    seq        bigint      NOT NULL,
    type       text        NOT NULL CHECK (type IN ('trace', 'message', 'status')),
    staff      jsonb       NOT NULL,
    public     jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (ticket_id, seq)
);

-- Один рядок на тікет → тікет ніколи не обробляється двома воркерами одночасно.
CREATE TABLE support_jobs (
    ticket_id    uuid        PRIMARY KEY REFERENCES support_tickets (id) ON DELETE CASCADE,
    state        text        NOT NULL DEFAULT 'queued' CHECK (state IN ('queued', 'running')),
    dirty        boolean     NOT NULL DEFAULT false,
    attempts     smallint    NOT NULL DEFAULT 0,
    run_after    timestamptz NOT NULL DEFAULT now(),
    locked_until timestamptz,
    last_error   text        CHECK (last_error IS NULL OR char_length(last_error) <= 200)
);
CREATE INDEX support_jobs_due_idx ON support_jobs (run_after) WHERE state = 'queued';

CREATE TABLE support_decisions (
    id                uuid        PRIMARY KEY,
    ticket_id         uuid        NOT NULL REFERENCES support_tickets (id) ON DELETE CASCADE,
    classification    jsonb       NOT NULL,
    policy_rule       text        NOT NULL,
    final_rule        text        NOT NULL,
    autonomy          text        NOT NULL CHECK (autonomy IN ('auto', 'approve', 'escalate')),
    decision          jsonb       NOT NULL,
    checks            jsonb       NOT NULL DEFAULT '[]',
    model             text        NOT NULL DEFAULT '',
    prompt_tokens     integer     NOT NULL DEFAULT 0,
    completion_tokens integer     NOT NULL DEFAULT 0,
    latency_ms        integer     NOT NULL DEFAULT 0,
    created_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX support_decisions_ticket_idx ON support_decisions (ticket_id, created_at);
CREATE INDEX support_decisions_created_idx ON support_decisions (created_at);

CREATE TABLE support_actions (
    id              uuid        PRIMARY KEY,
    ticket_id       uuid        NOT NULL REFERENCES support_tickets (id) ON DELETE CASCADE,
    type            text        NOT NULL CHECK (type IN ('restore_idea', 'notify_staff', 'bug_report', 'data_request')),
    idea_id         uuid        REFERENCES ideas (id) ON DELETE SET NULL,
    params          jsonb       NOT NULL DEFAULT '{}',
    status          text        NOT NULL DEFAULT 'pending'
                                CHECK (status IN ('pending', 'approved', 'rejected', 'executed', 'failed')),
    idempotency_key text        NOT NULL,
    rule            text        NOT NULL,
    evidence        jsonb       NOT NULL DEFAULT '[]',
    draft_reply     text        NOT NULL DEFAULT '',
    final_reply     text,
    edited          boolean     NOT NULL DEFAULT false,
    reason          text        CHECK (reason IS NULL OR char_length(reason) <= 1000),
    decided_by      uuid        REFERENCES users (id) ON DELETE SET NULL,
    decided_at      timestamptz,
    error           text        CHECK (error IS NULL OR char_length(error) <= 200),
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT support_actions_key UNIQUE (idempotency_key),
    CONSTRAINT support_actions_decided CHECK (status = 'pending' OR decided_at IS NOT NULL)
);
CREATE UNIQUE INDEX support_actions_one_pending_idx ON support_actions (ticket_id) WHERE status = 'pending';
CREATE INDEX support_actions_idea_idx ON support_actions (idea_id) WHERE idea_id IS NOT NULL;
CREATE INDEX support_actions_decided_by_idx ON support_actions (decided_by) WHERE decided_by IS NOT NULL;

-- Переживає видалення акаунта: виконання запиту на видалення має лишатися доказовим.
CREATE TABLE support_data_requests (
    id           uuid        PRIMARY KEY,
    ticket_id    uuid        REFERENCES support_tickets (id) ON DELETE SET NULL,
    user_id      uuid        REFERENCES users (id) ON DELETE SET NULL,
    kind         text        NOT NULL CHECK (kind IN ('access', 'erasure', 'rectification', 'objection')),
    status       text        NOT NULL DEFAULT 'received' CHECK (status IN ('received', 'completed', 'rejected')),
    due_at       timestamptz NOT NULL,
    decided_by   uuid        REFERENCES users (id) ON DELETE SET NULL,
    note         text        CHECK (note IS NULL OR char_length(note) <= 1000),
    completed_at timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX support_data_requests_open_idx ON support_data_requests (due_at) WHERE status = 'received';
CREATE INDEX support_data_requests_ticket_idx ON support_data_requests (ticket_id) WHERE ticket_id IS NOT NULL;
CREATE INDEX support_data_requests_user_idx ON support_data_requests (user_id) WHERE user_id IS NOT NULL;
CREATE INDEX support_data_requests_decided_by_idx ON support_data_requests (decided_by) WHERE decided_by IS NOT NULL;

CREATE TABLE support_feedback (
    message_id uuid        PRIMARY KEY REFERENCES support_messages (id) ON DELETE CASCADE,
    helpful    boolean     NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
