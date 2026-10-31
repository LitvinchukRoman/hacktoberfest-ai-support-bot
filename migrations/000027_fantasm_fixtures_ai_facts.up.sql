-- Standalone-repo stand-in ONLY, like 000001, 000025 and 000026. The AI core reads a person's
-- sign-ins, their ideas, the reports on them and push delivery. In the real fantasm database
-- these columns and tables already exist (migrations 1-22) under the same names; this file
-- adds just enough of them for the facts queries and the demo seed to run here.
ALTER TABLE users
    ADD COLUMN name           text    NOT NULL DEFAULT '',
    ADD COLUMN email          text    NOT NULL DEFAULT '',
    ADD COLUMN approved_ideas integer NOT NULL DEFAULT 0;

ALTER TABLE ideas
    ADD COLUMN summary      text        NOT NULL DEFAULT '',
    ADD COLUMN category     text        NOT NULL DEFAULT 'PROJECT',
    ADD COLUMN status       text        NOT NULL DEFAULT 'OPEN',
    ADD COLUMN visibility   text        NOT NULL DEFAULT 'PUBLIC',
    ADD COLUMN published_at timestamptz,
    ADD COLUMN deleted_at   timestamptz,
    ADD COLUMN updated_at   timestamptz NOT NULL DEFAULT now();

ALTER TABLE moderation_cases
    ADD COLUMN decision   text,
    ADD COLUMN decided_at timestamptz;

CREATE TABLE external_identities (
    provider       text    NOT NULL,
    issuer         text    NOT NULL,
    subject        text    NOT NULL,
    user_id        uuid    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    tenant_id      text    NOT NULL DEFAULT '',
    email          text    NOT NULL DEFAULT '',
    email_verified boolean NOT NULL DEFAULT false,
    PRIMARY KEY (issuer, subject)
);
CREATE INDEX external_identities_user_id_idx ON external_identities (user_id);

CREATE TABLE reports (
    idea_id     uuid        NOT NULL REFERENCES ideas (id) ON DELETE CASCADE,
    user_id     uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    reason      text        NOT NULL,
    status      text        NOT NULL DEFAULT 'OPEN' CHECK (status IN ('OPEN', 'RESOLVED')),
    created_at  timestamptz NOT NULL DEFAULT now(),
    resolved_at timestamptz,
    PRIMARY KEY (idea_id, user_id)
);
CREATE INDEX reports_user_idx ON reports (user_id);

CREATE TABLE push_subscriptions (
    id         uuid        PRIMARY KEY,
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    endpoint   text        NOT NULL UNIQUE,
    p256dh     text        NOT NULL,
    auth       text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX push_subscriptions_user_idx ON push_subscriptions (user_id, created_at DESC);

CREATE TABLE notifications (
    id           uuid        PRIMARY KEY,
    user_id      uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    type         text        NOT NULL,
    payload      jsonb       NOT NULL DEFAULT '{}',
    read_at      timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    push_sent_at timestamptz
);
CREATE INDEX notifications_user_idx ON notifications (user_id, created_at DESC);
