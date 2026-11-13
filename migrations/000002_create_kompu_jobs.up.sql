CREATE TABLE public.kompu_jobs (
   id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
   activity_date  DATE        NOT NULL,
   title          TEXT        NOT NULL,
   objective      TEXT        NOT NULL DEFAULT '',
   location       TEXT        NOT NULL DEFAULT '',
   participants   TEXT        NOT NULL DEFAULT '',
   evidence_files JSONB       NOT NULL DEFAULT '[]'::jsonb,
   created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
   updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

   CONSTRAINT kompu_jobs_title_not_blank
       CHECK (btrim(title) <> ''),
   CONSTRAINT kompu_jobs_evidence_files_is_array
       CHECK (jsonb_typeof(evidence_files) = 'array')
);

-- Default list order and date-range filters.
CREATE INDEX idx_kompu_jobs_activity_date
    ON public.kompu_jobs (activity_date DESC, created_at DESC);

CREATE TRIGGER trg_kompu_jobs_updated_at
    BEFORE UPDATE ON public.kompu_jobs
    FOR EACH ROW EXECUTE FUNCTION public.kompu_set_updated_at();

-- Supabase exposes public tables through its REST API. With RLS enabled and
-- no policies, the anon/authenticated roles get NO access. Only this backend
-- (connecting as the postgres role) can read and write.
ALTER TABLE public.kompu_jobs ENABLE ROW LEVEL SECURITY;