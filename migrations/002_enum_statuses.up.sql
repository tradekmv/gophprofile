-- 002: привести upload_status и processing_status к ENUM-типам.
-- Для уже существующих данных сохраняем значения в новых колонках.

DO $$ BEGIN
    CREATE TYPE avatar_upload_status AS ENUM ('uploading', 'uploaded');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    CREATE TYPE avatar_processing_status AS ENUM ('pending', 'processing', 'completed', 'failed');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

-- Меняем колонки (если они ещё VARCHAR).
DO $$ BEGIN
    IF (SELECT data_type FROM information_schema.columns
        WHERE table_name='avatars' AND column_name='upload_status') = 'character varying' THEN
        ALTER TABLE avatars
            ALTER COLUMN upload_status DROP DEFAULT,
            ALTER COLUMN upload_status TYPE avatar_upload_status USING upload_status::avatar_upload_status,
            ALTER COLUMN upload_status SET DEFAULT 'uploaded'::avatar_upload_status;
    END IF;
END $$;

DO $$ BEGIN
    IF (SELECT data_type FROM information_schema.columns
        WHERE table_name='avatars' AND column_name='processing_status') = 'character varying' THEN
        ALTER TABLE avatars
            ALTER COLUMN processing_status DROP DEFAULT,
            ALTER COLUMN processing_status TYPE avatar_processing_status USING processing_status::avatar_processing_status,
            ALTER COLUMN processing_status SET DEFAULT 'pending'::avatar_processing_status;
    END IF;
END $$;
