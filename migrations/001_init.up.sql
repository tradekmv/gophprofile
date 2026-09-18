CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- Тип для upload_status: фиксированный набор значений.
DO $$ BEGIN
    CREATE TYPE avatar_upload_status AS ENUM ('uploading', 'uploaded');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

-- Тип для processing_status.
DO $$ BEGIN
    CREATE TYPE avatar_processing_status AS ENUM ('pending', 'processing', 'completed', 'failed');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

CREATE TABLE IF NOT EXISTS avatars (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id VARCHAR(255) NOT NULL,
    file_name VARCHAR(255) NOT NULL,
    mime_type VARCHAR(100) NOT NULL,
    size_bytes BIGINT NOT NULL,
    s3_key VARCHAR(500) NOT NULL,
    thumbnail_s3_keys JSONB,
    upload_status avatar_upload_status NOT NULL DEFAULT 'uploaded',
    processing_status avatar_processing_status NOT NULL DEFAULT 'pending',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_avatars_user_id
    ON avatars(user_id)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_avatars_status
    ON avatars(upload_status, processing_status);
