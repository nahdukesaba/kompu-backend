CREATE TABLE public.kompu_releases (
   id                   UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
   start_date           DATE        NOT NULL,
   title                TEXT        NOT NULL,
   progress_status      TEXT        NOT NULL,
   social_media_status  JSONB       NOT NULL DEFAULT '{}'::jsonb,
   sharepoint_folder_url TEXT       NOT NULL DEFAULT '',
   attachment_urls      JSONB       NOT NULL DEFAULT '[]'::jsonb,
   caption              TEXT        NOT NULL DEFAULT '',
   created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
   updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),

   CONSTRAINT kompu_releases_title_not_blank
       CHECK (btrim(title) <> ''),
   CONSTRAINT kompu_releases_progress_status_not_blank
       CHECK (btrim(progress_status) <> ''),
   CONSTRAINT kompu_releases_attachment_urls_is_array
       CHECK (jsonb_typeof(attachment_urls) = 'array')
);

-- Default list order and date-range filters.
CREATE INDEX idx_kompu_releases_start_date
    ON public.kompu_releases (start_date DESC, created_at DESC);

-- Filter by progress status.
CREATE INDEX idx_kompu_releases_progress_status
    ON public.kompu_releases (progress_status);

-- Supports JSONB containment filters, e.g. social_media_status @> '{"instagram":"posted"}'.
CREATE INDEX idx_kompu_releases_social_media_status
    ON public.kompu_releases USING GIN (social_media_status jsonb_path_ops);

CREATE TRIGGER trg_kompu_releases_updated_at
    BEFORE UPDATE ON public.kompu_releases
    FOR EACH ROW EXECUTE FUNCTION public.kompu_set_updated_at();

ALTER TABLE public.kompu_releases ENABLE ROW LEVEL SECURITY;