-- General work for Subbagian Umum dan Tata Usaha that is never published, such
-- as designing staff cards, banners, and spanduk.
ALTER TABLE public.kompu_items DROP CONSTRAINT kompu_items_kind_check;
ALTER TABLE public.kompu_items
    ADD CONSTRAINT kompu_items_kind_check CHECK (kind IN ('documentation', 'publication', 'other'));
