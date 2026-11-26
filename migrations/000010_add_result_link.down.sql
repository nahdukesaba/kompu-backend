-- Drops every saved SharePoint folder link.
ALTER TABLE public.kompu_items DROP CONSTRAINT kompu_items_result_link_kind;
ALTER TABLE public.kompu_items DROP COLUMN result_link;
