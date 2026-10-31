ALTER TABLE support_messages ADD COLUMN signals text[] NOT NULL DEFAULT '{}';
ALTER TABLE support_tickets ADD COLUMN idea_slug text NOT NULL DEFAULT '' CHECK (char_length(idea_slug) <= 200);
