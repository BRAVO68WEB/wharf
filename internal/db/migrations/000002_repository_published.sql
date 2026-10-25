-- 000002_repository_published: a repository is hidden from the public
-- catalog until an operator publishes it. Existing rows stay unpublished.

ALTER TABLE repositories ADD COLUMN published INTEGER NOT NULL DEFAULT 0;
