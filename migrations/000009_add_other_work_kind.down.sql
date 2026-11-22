-- Fails while 'other' rows exist, so a rollback never deletes tracked work.
ALTER TABLE public.kompu_items DROP CONSTRAINT kompu_items_kind_check;
ALTER TABLE public.kompu_items
    ADD CONSTRAINT kompu_items_kind_check CHECK (kind IN ('documentation', 'publication'));
