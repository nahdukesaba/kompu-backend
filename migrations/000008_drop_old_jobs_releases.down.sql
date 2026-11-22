DO $$ BEGIN
    RAISE EXCEPTION '000008 is irreversible: the dropped test rows cannot be restored';
END $$;
