-- Tokens can expire. Existing tokens never do.
ALTER TABLE tokens ADD COLUMN expires_at INTEGER;

-- Active token names are unique, ignoring case. Older duplicates keep their name
-- only on the oldest token; the rest get their id appended.
UPDATE tokens SET name = name || ' (' || substr(id, 1, 6) || ')'
WHERE revoked_at IS NULL AND EXISTS (
    SELECT 1 FROM tokens AS o
    WHERE o.revoked_at IS NULL AND o.name = tokens.name COLLATE NOCASE
      AND (o.created_at < tokens.created_at OR (o.created_at = tokens.created_at AND o.id < tokens.id))
);

CREATE UNIQUE INDEX tokens_active_name_idx ON tokens (name COLLATE NOCASE) WHERE revoked_at IS NULL;
