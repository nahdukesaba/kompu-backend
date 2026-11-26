-- Documentation results can live in a SharePoint folder instead of uploads.
-- The app allows only one of the two per item.
ALTER TABLE public.kompu_items ADD COLUMN result_link TEXT NOT NULL DEFAULT '';
ALTER TABLE public.kompu_items
    ADD CONSTRAINT kompu_items_result_link_kind CHECK (kind = 'documentation' OR result_link = '');
