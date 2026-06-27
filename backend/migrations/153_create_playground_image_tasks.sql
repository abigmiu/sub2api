CREATE TABLE IF NOT EXISTS playground_image_tasks (
    id VARCHAR(64) PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status VARCHAR(20) NOT NULL,
    request_path VARCHAR(64) NOT NULL,
    request_content_type TEXT NOT NULL DEFAULT '',
    request_body BYTEA NOT NULL,
    error_message TEXT NOT NULL DEFAULT '',
    result_json JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_playground_image_tasks_user_created_at
    ON playground_image_tasks (user_id, created_at DESC);
