-- Newest Microsoft Graph refresh token for OneDrive file storage (row
-- 'kompu-onedrive'). The table is shared with SILAPET in this database, so it
-- is only created when missing. RLS with no policies keeps it away from the
-- Supabase anon/authenticated API roles; only backends read it.
CREATE TABLE IF NOT EXISTS public.graph_tokens (
    id            text PRIMARY KEY,
    refresh_token text NOT NULL,
    updated_at    timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE public.graph_tokens ENABLE ROW LEVEL SECURITY;
