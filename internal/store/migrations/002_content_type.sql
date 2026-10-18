-- Artifacts can be any file type; everything uploaded before this was HTML.
ALTER TABLE docs ADD COLUMN content_type TEXT NOT NULL DEFAULT 'text/html';
