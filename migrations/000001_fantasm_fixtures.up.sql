-- Standalone-repo stand-in ONLY. In the real fantasm monorepo these tables
-- already exist (migrations 1-22, owned by the main product, not by either
-- hackathon backend role). Kept intentionally tiny: just enough shape for
-- support_tickets' foreign keys to resolve and for local integration tests.
CREATE TABLE users (
    id         uuid        PRIMARY KEY,
    handle     text        NOT NULL,
    role       text        NOT NULL DEFAULT 'USER' CHECK (role IN ('USER', 'MODERATOR', 'ADMIN')),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE ideas (
    id         uuid        PRIMARY KEY,
    author_id  uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    slug       text        NOT NULL UNIQUE,
    title      text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
