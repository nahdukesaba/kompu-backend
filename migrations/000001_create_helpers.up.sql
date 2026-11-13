-- Shared trigger function: keeps updated_at current on every UPDATE.
CREATE OR REPLACE FUNCTION public.kompu_set_updated_at()
RETURNS TRIGGER
LANGUAGE plpgsql
SET search_path = ''
AS $$
BEGIN
    NEW.updated_at = now();
RETURN NEW;
END;
$$;