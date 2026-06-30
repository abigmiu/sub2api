ALTER TABLE playground_image_tasks
    ADD COLUMN IF NOT EXISTS request_headers JSONB;
