-- Audit columns + soft delete for both tables.
-- Existing rows are backfilled with 'system' as creator/updater.

-- ---------- kompu_jobs ----------
ALTER TABLE public.kompu_jobs
    ADD COLUMN created_by TEXT NOT NULL DEFAULT 'system',
    ADD COLUMN updated_by TEXT NOT NULL DEFAULT 'system',
    ADD COLUMN deleted_at TIMESTAMPTZ,
    ADD COLUMN deleted_by TEXT;

ALTER TABLE public.kompu_jobs
    ALTER COLUMN created_by DROP DEFAULT,
ALTER COLUMN updated_by DROP DEFAULT,
    ADD CONSTRAINT kompu_jobs_soft_delete_consistent
        CHECK ((deleted_at IS NULL) = (deleted_by IS NULL));

DROP INDEX public.idx_kompu_jobs_activity_date;
CREATE INDEX idx_kompu_jobs_activity_date
    ON public.kompu_jobs (activity_date DESC, created_at DESC)
    WHERE deleted_at IS NULL;

-- ---------- kompu_releases ----------
ALTER TABLE public.kompu_releases
    ADD COLUMN created_by TEXT NOT NULL DEFAULT 'system',
    ADD COLUMN updated_by TEXT NOT NULL DEFAULT 'system',
    ADD COLUMN deleted_at TIMESTAMPTZ,
    ADD COLUMN deleted_by TEXT;

ALTER TABLE public.kompu_releases
    ALTER COLUMN created_by DROP DEFAULT,
ALTER COLUMN updated_by DROP DEFAULT,
    ADD CONSTRAINT kompu_releases_soft_delete_consistent
        CHECK ((deleted_at IS NULL) = (deleted_by IS NULL));

DROP INDEX public.idx_kompu_releases_start_date;
DROP INDEX public.idx_kompu_releases_progress_status;
DROP INDEX public.idx_kompu_releases_social_media_status;

CREATE INDEX idx_kompu_releases_start_date
    ON public.kompu_releases (start_date DESC, created_at DESC)
    WHERE deleted_at IS NULL;
CREATE INDEX idx_kompu_releases_progress_status
    ON public.kompu_releases (progress_status)
    WHERE deleted_at IS NULL;
CREATE INDEX idx_kompu_releases_social_media_status
    ON public.kompu_releases USING GIN (social_media_status jsonb_path_ops)
    WHERE deleted_at IS NULL;