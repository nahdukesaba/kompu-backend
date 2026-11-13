-- WARNING: reverting makes soft-deleted rows visible again and drops audit data.

DROP INDEX public.idx_kompu_releases_social_media_status;
DROP INDEX public.idx_kompu_releases_progress_status;
DROP INDEX public.idx_kompu_releases_start_date;
CREATE INDEX idx_kompu_releases_start_date
    ON public.kompu_releases (start_date DESC, created_at DESC);
CREATE INDEX idx_kompu_releases_progress_status
    ON public.kompu_releases (progress_status);
CREATE INDEX idx_kompu_releases_social_media_status
    ON public.kompu_releases USING GIN (social_media_status jsonb_path_ops);
ALTER TABLE public.kompu_releases
DROP CONSTRAINT kompu_releases_soft_delete_consistent,
    DROP COLUMN deleted_by,
    DROP COLUMN deleted_at,
    DROP COLUMN updated_by,
    DROP COLUMN created_by;

DROP INDEX public.idx_kompu_jobs_activity_date;
CREATE INDEX idx_kompu_jobs_activity_date
    ON public.kompu_jobs (activity_date DESC, created_at DESC);
ALTER TABLE public.kompu_jobs
DROP CONSTRAINT kompu_jobs_soft_delete_consistent,
    DROP COLUMN deleted_by,
    DROP COLUMN deleted_at,
    DROP COLUMN updated_by,
    DROP COLUMN created_by;