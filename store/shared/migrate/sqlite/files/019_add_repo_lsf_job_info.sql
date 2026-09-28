-- name: alter-table-repos-add-lsf-job-info-disabled

ALTER TABLE repos ADD COLUMN repo_lsf_job_info_disabled BOOLEAN NOT NULL DEFAULT FALSE;
