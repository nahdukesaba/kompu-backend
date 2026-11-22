-- The old jobs and releases modules held test data only and were replaced by
-- kompu_items. This permanently removes those rows.
DROP TABLE public.kompu_jobs;
DROP TABLE public.kompu_releases;
