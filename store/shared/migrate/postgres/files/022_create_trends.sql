-- name: create-trend-frames
CREATE TABLE trend_frames (
 trend_time BIGINT PRIMARY KEY,
 trend_lsf_valid INTEGER NOT NULL
);

-- name: create-trend-samples
CREATE TABLE trend_samples (
 trend_time BIGINT NOT NULL,
 trend_repo BIGINT NOT NULL,
 build_running INTEGER NOT NULL,
 build_pending INTEGER NOT NULL,
 lsf_running INTEGER NOT NULL,
 lsf_pending INTEGER NOT NULL,
 lsf_valid INTEGER NOT NULL,
 PRIMARY KEY (trend_time, trend_repo)
);

-- name: create-trend-jobs
CREATE TABLE trend_jobs (
 job_id VARCHAR(64) PRIMARY KEY,
 job_repo BIGINT NOT NULL,
 job_name VARCHAR(250) NOT NULL
);

-- name: create-trend-repo-index
CREATE INDEX trend_repo_time ON trend_samples(trend_repo,trend_time);
