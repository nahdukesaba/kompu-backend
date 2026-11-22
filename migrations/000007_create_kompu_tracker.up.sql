-- One work tracker for the Kompu team: documentation and publication items on
-- a single calendar. Uploaded files live in OneDrive; only their keys are here.

-- Social channels. Adding one is a single INSERT: the UI, posting toggles,
-- proof uploads and reports all read this table.
CREATE TABLE public.kompu_channels (
    code                TEXT    PRIMARY KEY CHECK (code ~ '^[a-z0-9_]{2,32}$'),
    label               TEXT    NOT NULL CHECK (btrim(label) <> ''),
    sort_order          INT     NOT NULL DEFAULT 0,
    is_active           BOOLEAN NOT NULL DEFAULT TRUE,
    in_quarterly_report BOOLEAN NOT NULL DEFAULT FALSE,
    in_monthly_report   BOOLEAN NOT NULL DEFAULT FALSE,
    uses_format         BOOLEAN NOT NULL DEFAULT FALSE, -- report splits it into Feed / Reels
    date_label          TEXT    NOT NULL DEFAULT 'Tanggal'
);

INSERT INTO public.kompu_channels
    (code, label, sort_order, in_quarterly_report, in_monthly_report, uses_format, date_label)
VALUES
    ('instagram', 'Instagram', 10, TRUE,  TRUE,  TRUE,  'Tanggal'),
    ('facebook',  'Facebook',  20, FALSE, FALSE, FALSE, 'Tanggal'),
    ('website',   'Website',   30, TRUE,  FALSE, FALSE, 'Tanggal Rilis');

CREATE TABLE public.kompu_items (
    id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    kind             TEXT        NOT NULL CHECK (kind IN ('documentation', 'publication')),
    title            TEXT        NOT NULL CHECK (btrim(title) <> ''),
    start_date       DATE        NOT NULL,
    end_date         DATE        NOT NULL,
    people           TEXT[]      NOT NULL DEFAULT '{}',
    -- documentation
    location         TEXT        NOT NULL DEFAULT '',
    objective        TEXT        NOT NULL DEFAULT '',
    report_text      TEXT        NOT NULL DEFAULT '',
    -- publication
    category         TEXT        NOT NULL DEFAULT '',
    status           TEXT        NOT NULL DEFAULT '',
    format           TEXT        NOT NULL DEFAULT 'feed' CHECK (format IN ('feed', 'reels')),
    caption          TEXT        NOT NULL DEFAULT '',
    documentation_id UUID        REFERENCES public.kompu_items (id) ON DELETE SET NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by       TEXT        NOT NULL,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by       TEXT        NOT NULL,
    deleted_at       TIMESTAMPTZ,
    deleted_by       TEXT,

    CONSTRAINT kompu_items_date_order CHECK (end_date >= start_date),
    CONSTRAINT kompu_items_doc_link CHECK (kind = 'publication' OR documentation_id IS NULL),
    CONSTRAINT kompu_items_soft_delete_consistent CHECK ((deleted_at IS NULL) = (deleted_by IS NULL))
);

CREATE INDEX idx_kompu_items_dates ON public.kompu_items (start_date, end_date) WHERE deleted_at IS NULL;
CREATE INDEX idx_kompu_items_documentation ON public.kompu_items (documentation_id) WHERE documentation_id IS NOT NULL;

CREATE TRIGGER trg_kompu_items_updated_at
    BEFORE UPDATE ON public.kompu_items
    FOR EACH ROW EXECUTE FUNCTION public.kompu_set_updated_at();

-- A row means "posted on this channel"; posted_on is the date used by reports.
CREATE TABLE public.kompu_item_channels (
    item_id      UUID NOT NULL REFERENCES public.kompu_items (id) ON DELETE CASCADE,
    channel_code TEXT NOT NULL REFERENCES public.kompu_channels (code),
    posted_on    DATE NOT NULL,
    PRIMARY KEY (item_id, channel_code)
);

CREATE INDEX idx_kompu_item_channels_posted_on ON public.kompu_item_channels (posted_on);

-- One row per file, so concurrent uploads never overwrite each other.
CREATE TABLE public.kompu_item_files (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    item_id      UUID        NOT NULL REFERENCES public.kompu_items (id) ON DELETE CASCADE,
    slot         TEXT        NOT NULL CHECK (slot IN ('report', 'capture_proof', 'result', 'post_proof')),
    channel_code TEXT        REFERENCES public.kompu_channels (code),
    key          TEXT        NOT NULL UNIQUE,
    name         TEXT        NOT NULL,
    content_type TEXT        NOT NULL DEFAULT '',
    size         BIGINT      NOT NULL DEFAULT 0 CHECK (size >= 0),
    created_by   TEXT        NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT kompu_item_files_channel_slot CHECK ((slot = 'post_proof') = (channel_code IS NOT NULL))
);

CREATE INDEX idx_kompu_item_files_item ON public.kompu_item_files (item_id, created_at);

-- National holidays and collective leave (SKB 3 Menteri). Next year's SKB is
-- a new INSERT.
CREATE TABLE public.kompu_holidays (
    date DATE NOT NULL,
    name TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('libur', 'cuti_bersama')),
    PRIMARY KEY (date, name)
);

INSERT INTO public.kompu_holidays (date, kind, name) VALUES
    -- 2026: SKB Nomor 1497 Tahun 2025, 2, 5
    ('2026-01-01', 'libur', 'Tahun Baru 2026 Masehi'),
    ('2026-01-16', 'libur', 'Isra Mikraj Nabi Muhammad SAW'),
    ('2026-02-16', 'cuti_bersama', 'Cuti Bersama Tahun Baru Imlek 2577 Kongzili'),
    ('2026-02-17', 'libur', 'Tahun Baru Imlek 2577 Kongzili'),
    ('2026-03-18', 'cuti_bersama', 'Cuti Bersama Hari Suci Nyepi'),
    ('2026-03-19', 'libur', 'Hari Suci Nyepi (Tahun Baru Saka 1948)'),
    ('2026-03-20', 'cuti_bersama', 'Cuti Bersama Idulfitri 1447 H'),
    ('2026-03-21', 'libur', 'Idulfitri 1447 H'),
    ('2026-03-22', 'libur', 'Idulfitri 1447 H'),
    ('2026-03-23', 'cuti_bersama', 'Cuti Bersama Idulfitri 1447 H'),
    ('2026-03-24', 'cuti_bersama', 'Cuti Bersama Idulfitri 1447 H'),
    ('2026-04-03', 'libur', 'Wafat Yesus Kristus'),
    ('2026-04-05', 'libur', 'Kebangkitan Yesus Kristus (Paskah)'),
    ('2026-05-01', 'libur', 'Hari Buruh Internasional'),
    ('2026-05-14', 'libur', 'Kenaikan Yesus Kristus'),
    ('2026-05-15', 'cuti_bersama', 'Cuti Bersama Kenaikan Yesus Kristus'),
    ('2026-05-27', 'libur', 'Iduladha 1447 H'),
    ('2026-05-28', 'cuti_bersama', 'Cuti Bersama Iduladha 1447 H'),
    ('2026-05-31', 'libur', 'Hari Raya Waisak 2570 BE'),
    ('2026-06-01', 'libur', 'Hari Lahir Pancasila'),
    ('2026-06-16', 'libur', '1 Muharam Tahun Baru Islam 1448 H'),
    ('2026-08-17', 'libur', 'Proklamasi Kemerdekaan'),
    ('2026-08-25', 'libur', 'Maulid Nabi Muhammad SAW'),
    ('2026-12-24', 'cuti_bersama', 'Cuti Bersama Kelahiran Yesus Kristus'),
    ('2026-12-25', 'libur', 'Kelahiran Yesus Kristus'),
    -- 2027: SKB Nomor 1205, 3, 2 Tahun 2026
    ('2027-01-01', 'libur', 'Tahun Baru 2027 Masehi'),
    ('2027-01-05', 'libur', 'Isra Mikraj Nabi Muhammad SAW 1448 H'),
    ('2027-02-05', 'cuti_bersama', 'Cuti Bersama Tahun Baru Imlek 2578 Kongzili'),
    ('2027-02-06', 'libur', 'Tahun Baru Imlek 2578 Kongzili'),
    ('2027-03-08', 'libur', 'Hari Suci Nyepi (Tahun Baru Saka 1949)'),
    ('2027-03-09', 'cuti_bersama', 'Cuti Bersama Idulfitri 1448 H'),
    ('2027-03-10', 'libur', 'Idulfitri 1448 H'),
    ('2027-03-11', 'libur', 'Idulfitri 1448 H'),
    ('2027-03-12', 'cuti_bersama', 'Cuti Bersama Idulfitri 1448 H'),
    ('2027-03-15', 'cuti_bersama', 'Cuti Bersama Idulfitri 1448 H'),
    ('2027-03-25', 'cuti_bersama', 'Cuti Bersama Wafat Yesus Kristus'),
    ('2027-03-26', 'libur', 'Wafat Yesus Kristus'),
    ('2027-03-28', 'libur', 'Kebangkitan Yesus Kristus (Paskah)'),
    ('2027-05-01', 'libur', 'Hari Buruh Internasional'),
    ('2027-05-06', 'libur', 'Kenaikan Yesus Kristus'),
    ('2027-05-17', 'libur', 'Iduladha 1448 H'),
    ('2027-05-18', 'cuti_bersama', 'Cuti Bersama Iduladha 1448 H'),
    ('2027-05-19', 'cuti_bersama', 'Cuti Bersama Hari Raya Waisak 2571 BE'),
    ('2027-05-20', 'libur', 'Hari Raya Waisak 2571 BE'),
    ('2027-06-01', 'libur', 'Hari Lahir Pancasila'),
    ('2027-06-06', 'libur', '1 Muharam Tahun Baru Islam 1449 H'),
    ('2027-08-15', 'libur', 'Maulid Nabi Muhammad SAW'),
    ('2027-08-17', 'libur', 'Proklamasi Kemerdekaan'),
    ('2027-12-24', 'cuti_bersama', 'Cuti Bersama Kelahiran Yesus Kristus'),
    ('2027-12-25', 'libur', 'Kelahiran Yesus Kristus'),
    ('2027-12-26', 'libur', 'Isra Mikraj Nabi Muhammad SAW 1449 H');

-- No policies: only this backend (postgres role) can read and write.
ALTER TABLE public.kompu_channels ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.kompu_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.kompu_item_channels ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.kompu_item_files ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.kompu_holidays ENABLE ROW LEVEL SECURITY;
