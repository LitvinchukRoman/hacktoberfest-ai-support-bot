ALTER TABLE ideas ADD COLUMN moderation_state text NOT NULL DEFAULT 'PENDING';

CREATE TABLE moderation_cases (
    id         uuid        PRIMARY KEY,
    idea_id    uuid        NOT NULL REFERENCES ideas (id) ON DELETE CASCADE,
    source     text        NOT NULL,
    state      text        NOT NULL,
    decided_by uuid        REFERENCES users (id) ON DELETE SET NULL,
    note       text,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX moderation_cases_idea_idx ON moderation_cases (idea_id);
