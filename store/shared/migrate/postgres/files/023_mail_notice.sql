-- name: alter-table-repos-add-mail-notice

ALTER TABLE repos ADD COLUMN repo_mail_notice BOOLEAN NOT NULL DEFAULT FALSE;

-- name: alter-table-repos-add-mail-to

ALTER TABLE repos ADD COLUMN repo_mail_to VARCHAR(2048) NOT NULL DEFAULT '';

-- name: alter-table-repos-add-mail-cc

ALTER TABLE repos ADD COLUMN repo_mail_cc VARCHAR(2048) NOT NULL DEFAULT '';

-- name: create-table-mail-notices

CREATE TABLE mail_notices (build_id BIGINT PRIMARY KEY);
