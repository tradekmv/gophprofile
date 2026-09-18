DO $$ BEGIN
    ALTER TABLE avatars
        ALTER COLUMN upload_status DROP DEFAULT,
        ALTER COLUMN upload_status TYPE VARCHAR(50) USING upload_status::VARCHAR,
        ALTER COLUMN upload_status SET DEFAULT 'uploaded';
EXCEPTION WHEN OTHERS THEN NULL; END $$;

DO $$ BEGIN
    ALTER TABLE avatars
        ALTER COLUMN processing_status DROP DEFAULT,
        ALTER COLUMN processing_status TYPE VARCHAR(50) USING processing_status::VARCHAR,
        ALTER COLUMN processing_status SET DEFAULT 'pending';
EXCEPTION WHEN OTHERS THEN NULL; END $$;

DROP TYPE IF EXISTS avatar_upload_status;
DROP TYPE IF EXISTS avatar_processing_status;
