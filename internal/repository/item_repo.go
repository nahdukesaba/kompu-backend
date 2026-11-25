package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"kompu-backend/internal/model"
)

// itemDB is satisfied by *pgxpool.Pool and pgx.Tx, so the integration test can
// run every query inside a transaction it rolls back.
type itemDB interface {
	Begin(ctx context.Context) (pgx.Tx, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type ItemRepository struct {
	db itemDB
}

func NewItemRepository(db itemDB) *ItemRepository { return &ItemRepository{db: db} }

const itemSelectTemplate = `
SELECT i.id::text, i.kind, i.title, i.start_date::text, i.end_date::text, i.people,
       i.location, i.objective, %s, i.category, i.status, i.format, i.caption,
       i.documentation_id::text,
       COALESCE((SELECT jsonb_object_agg(c.channel_code, c.posted_on::text)
                 FROM public.kompu_item_channels c WHERE c.item_id = i.id), '{}'::jsonb),
       COALESCE((SELECT jsonb_agg(jsonb_build_object(
                     'id', f.id, 'slot', f.slot, 'channel_code', f.channel_code, 'key', f.key,
                     'name', f.name, 'content_type', f.content_type, 'size', f.size)
                     ORDER BY f.created_at, f.id)
                 FROM public.kompu_item_files f WHERE f.item_id = i.id), '[]'::jsonb),
       i.created_at, i.created_by, i.updated_at, i.updated_by
FROM public.kompu_items i `

var (
	itemSelectFull = fmt.Sprintf(itemSelectTemplate, "i.report_text")
	// Calendar and report lists skip the transcript, which can be long.
	itemSelectLight = fmt.Sprintf(itemSelectTemplate, "''::text")
)

func scanItem(row pgx.Row) (*model.Item, error) {
	var it model.Item
	var channels, files []byte
	if err := row.Scan(&it.ID, &it.Kind, &it.Title, &it.StartDate, &it.EndDate, &it.People,
		&it.Location, &it.Objective, &it.ReportText, &it.Category, &it.Status, &it.Format, &it.Caption,
		&it.DocumentationID, &channels, &files,
		&it.CreatedAt, &it.CreatedBy, &it.UpdatedAt, &it.UpdatedBy); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(channels, &it.Channels); err != nil {
		return nil, fmt.Errorf("decode item channels: %w", err)
	}
	if err := json.Unmarshal(files, &it.Files); err != nil {
		return nil, fmt.Errorf("decode item files: %w", err)
	}
	for i := range it.Files {
		it.Files[i].URL = FilesRoute + it.Files[i].Key
	}
	return &it, nil
}

func (r *ItemRepository) queryItems(ctx context.Context, sql string, args ...any) ([]*model.Item, error) {
	rows, err := r.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("query items: %w", err)
	}
	defer rows.Close()

	items := []*model.Item{}
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, fmt.Errorf("scan item: %w", err)
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// List returns live items overlapping [from, to].
// shortcut: capped at 3000 rows, far above one month of work; page it if a range ever hits the cap.
func (r *ItemRepository) List(ctx context.Context, from, to string) ([]*model.Item, error) {
	return r.queryItems(ctx, itemSelectLight+`
		WHERE i.deleted_at IS NULL AND i.start_date <= $2::text::date AND i.end_date >= $1::text::date
		ORDER BY i.start_date, i.created_at, i.id LIMIT 3000`, from, to)
}

// Report returns documentation that started in [from, to] and publications
// posted on any channel in [from, to].
func (r *ItemRepository) Report(ctx context.Context, from, to string) ([]*model.Item, error) {
	return r.queryItems(ctx, itemSelectLight+`
		WHERE i.deleted_at IS NULL AND (
			(i.kind = 'documentation' AND i.start_date BETWEEN $1::text::date AND $2::text::date)
			OR (i.kind = 'publication' AND EXISTS (
				SELECT 1 FROM public.kompu_item_channels c
				WHERE c.item_id = i.id AND c.posted_on BETWEEN $1::text::date AND $2::text::date))
		)
		ORDER BY i.start_date, i.created_at, i.id`, from, to)
}

func (r *ItemRepository) Get(ctx context.Context, id string) (*model.Item, error) {
	it, err := scanItem(r.db.QueryRow(ctx, itemSelectFull+`WHERE i.id = $1::text::uuid AND i.deleted_at IS NULL`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, model.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get item: %w", err)
	}
	return it, nil
}

// Create inserts all items atomically (one per day when split).
func (r *ItemRepository) Create(ctx context.Context, items []*model.Item) ([]*model.Item, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin create items: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	ids := make([]string, 0, len(items))
	for _, it := range items {
		var id string
		if err := tx.QueryRow(ctx, `
			INSERT INTO public.kompu_items
				(kind, title, start_date, end_date, people, location, objective, report_text,
				 category, status, format, caption, documentation_id, created_by, updated_by)
			VALUES ($1, $2, $3::text::date, $4::text::date, $5::text[], $6, $7, $8,
				$9, $10, $11, $12, $13::text::uuid, $14, $14)
			RETURNING id::text`,
			it.Kind, it.Title, it.StartDate, it.EndDate, it.People, it.Location, it.Objective, it.ReportText,
			it.Category, it.Status, it.Format, it.Caption, it.DocumentationID, it.CreatedBy).Scan(&id); err != nil {
			return nil, fmt.Errorf("insert item: %w", err)
		}
		ids = append(ids, id)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit items: %w", err)
	}
	return r.queryItems(ctx, itemSelectLight+`WHERE i.id = ANY($1::text[]::uuid[]) ORDER BY i.start_date, i.id`, ids)
}

func (r *ItemRepository) Update(ctx context.Context, it *model.Item) (*model.Item, error) {
	tag, err := r.db.Exec(ctx, `
		UPDATE public.kompu_items
		SET title = $2, start_date = $3::text::date, end_date = $4::text::date, people = $5::text[],
		    location = $6, objective = $7, report_text = $8, category = $9, status = $10,
		    format = $11, caption = $12, documentation_id = $13::text::uuid, updated_by = $14
		WHERE id = $1::text::uuid AND deleted_at IS NULL`,
		it.ID, it.Title, it.StartDate, it.EndDate, it.People, it.Location, it.Objective, it.ReportText,
		it.Category, it.Status, it.Format, it.Caption, it.DocumentationID, it.UpdatedBy)
	if err != nil {
		return nil, fmt.Errorf("update item: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, model.ErrNotFound
	}
	return r.Get(ctx, it.ID)
}

func (r *ItemRepository) Reschedule(ctx context.Context, id, start, end, actor string) (*model.Item, error) {
	tag, err := r.db.Exec(ctx, `
		UPDATE public.kompu_items SET start_date = $2::text::date, end_date = $3::text::date, updated_by = $4
		WHERE id = $1::text::uuid AND deleted_at IS NULL`, id, start, end, actor)
	if err != nil {
		return nil, fmt.Errorf("reschedule item: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, model.ErrNotFound
	}
	return r.Get(ctx, id)
}

// SetChannel marks a publication as posted on a channel.
func (r *ItemRepository) SetChannel(ctx context.Context, id, code, postedOn, actor string) error {
	tag, err := r.db.Exec(ctx, `
		WITH target AS (
			UPDATE public.kompu_items SET updated_by = $4
			WHERE id = $1::text::uuid AND kind = 'publication' AND deleted_at IS NULL
			RETURNING id
		)
		INSERT INTO public.kompu_item_channels (item_id, channel_code, posted_on)
		SELECT id, $2::text, $3::text::date FROM target
		ON CONFLICT (item_id, channel_code) DO UPDATE SET posted_on = EXCLUDED.posted_on`,
		id, code, postedOn, actor)
	if err != nil {
		return fmt.Errorf("set channel: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return model.ErrNotFound
	}
	return nil
}

// UnsetChannel marks a publication as not posted. Its proof files stay.
func (r *ItemRepository) UnsetChannel(ctx context.Context, id, code, actor string) error {
	_, err := r.db.Exec(ctx, `
		WITH target AS (
			UPDATE public.kompu_items SET updated_by = $3
			WHERE id = $1::text::uuid AND kind = 'publication' AND deleted_at IS NULL
			RETURNING id
		)
		DELETE FROM public.kompu_item_channels c USING target
		WHERE c.item_id = target.id AND c.channel_code = $2::text`, id, code, actor)
	if err != nil {
		return fmt.Errorf("unset channel: %w", err)
	}
	return nil
}

// AddFiles attaches uploaded files. A key already attached is ignored, so a
// retried request cannot duplicate a file.
func (r *ItemRepository) AddFiles(ctx context.Context, id, actor string, files []model.ItemFile) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin add files: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	for _, f := range files {
		if _, err := tx.Exec(ctx, `
			WITH target AS (
				UPDATE public.kompu_items SET updated_by = $8
				WHERE id = $1::text::uuid AND deleted_at IS NULL
				RETURNING id
			)
			INSERT INTO public.kompu_item_files (item_id, slot, channel_code, key, name, content_type, size, created_by)
			SELECT id, $2::text, $3::text, $4::text, $5::text, $6::text, $7::bigint, $8::text FROM target
			ON CONFLICT (key) DO NOTHING`,
			id, f.Slot, f.ChannelCode, f.Key, f.Name, f.ContentType, f.Size, actor); err != nil {
			return fmt.Errorf("add file: %w", err)
		}
	}
	return tx.Commit(ctx)
}

// DeleteFile removes the row and returns its key, for removal from storage.
func (r *ItemRepository) DeleteFile(ctx context.Context, id, fileID, actor string) (string, error) {
	var key string
	err := r.db.QueryRow(ctx, `
		WITH removed AS (
			DELETE FROM public.kompu_item_files f USING public.kompu_items i
			WHERE f.id = $2::text::uuid AND f.item_id = $1::text::uuid
			  AND i.id = f.item_id AND i.deleted_at IS NULL
			RETURNING f.key, f.item_id
		), touched AS (
			UPDATE public.kompu_items SET updated_by = $3 WHERE id IN (SELECT item_id FROM removed)
		)
		SELECT key FROM removed`, id, fileID, actor).Scan(&key)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", model.ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("delete file: %w", err)
	}
	return key, nil
}

// SoftDelete hides the item and unlinks publications that pointed to it.
func (r *ItemRepository) SoftDelete(ctx context.Context, id, actor string) error {
	var n int
	err := r.db.QueryRow(ctx, `
		WITH deleted AS (
			UPDATE public.kompu_items SET deleted_at = now(), deleted_by = $2, updated_by = $2
			WHERE id = $1::text::uuid AND deleted_at IS NULL
			RETURNING id
		), unlinked AS (
			UPDATE public.kompu_items SET documentation_id = NULL
			WHERE documentation_id IN (SELECT id FROM deleted)
		)
		SELECT count(*) FROM deleted`, id, actor).Scan(&n)
	if err != nil {
		return fmt.Errorf("delete item: %w", err)
	}
	if n == 0 {
		return model.ErrNotFound
	}
	return nil
}

func (r *ItemRepository) IsDocumentation(ctx context.Context, id string) (bool, error) {
	var ok bool
	err := r.db.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM public.kompu_items
		WHERE id = $1::text::uuid AND kind = 'documentation' AND deleted_at IS NULL)`, id).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("check documentation: %w", err)
	}
	return ok, nil
}

func (r *ItemRepository) Channels(ctx context.Context) ([]model.Channel, error) {
	rows, err := r.db.Query(ctx, `
		SELECT code, label, sort_order, in_quarterly_report, in_monthly_report, uses_format, date_label
		FROM public.kompu_channels WHERE is_active ORDER BY sort_order, code`)
	if err != nil {
		return nil, fmt.Errorf("list channels: %w", err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (model.Channel, error) {
		var c model.Channel
		err := row.Scan(&c.Code, &c.Label, &c.SortOrder, &c.InQuarterlyReport, &c.InMonthlyReport, &c.UsesFormat, &c.DateLabel)
		return c, err
	})
}

func (r *ItemRepository) Holidays(ctx context.Context, from, to string) ([]model.Holiday, error) {
	rows, err := r.db.Query(ctx, `
		SELECT date::text, name, kind FROM public.kompu_holidays
		WHERE date BETWEEN $1::text::date AND $2::text::date ORDER BY date, name`, from, to)
	if err != nil {
		return nil, fmt.Errorf("list holidays: %w", err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (model.Holiday, error) {
		var h model.Holiday
		err := row.Scan(&h.Date, &h.Name, &h.Kind)
		return h, err
	})
}

// People returns names used before, for autocomplete.
func (r *ItemRepository) People(ctx context.Context) ([]string, error) {
	rows, err := r.db.Query(ctx, `
		SELECT DISTINCT p FROM public.kompu_items i, unnest(i.people) AS p
		WHERE i.deleted_at IS NULL ORDER BY p LIMIT 300`)
	if err != nil {
		return nil, fmt.Errorf("list people: %w", err)
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}
