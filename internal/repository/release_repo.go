package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"kompu-backend/internal/model"
)

const releaseColumns = `id::text, start_date, title, progress_status, social_media_status,
	sharepoint_folder_url, attachment_urls, caption, created_at, created_by, updated_at, updated_by,
	deleted_at, deleted_by`

type PostgresReleaseRepository struct {
	db *pgxpool.Pool
}

func NewReleaseRepository(db *pgxpool.Pool) *PostgresReleaseRepository {
	return &PostgresReleaseRepository{db: db}
}

func releaseJSON(r *model.Release) (social, attachments []byte, err error) {
	if social, err = json.Marshal(r.SocialMediaStatus); err != nil {
		return nil, nil, fmt.Errorf("marshal social media status: %w", err)
	}
	if attachments, err = json.Marshal(r.Attachments); err != nil {
		return nil, nil, fmt.Errorf("marshal attachments: %w", err)
	}
	return social, attachments, nil
}

func (r *PostgresReleaseRepository) Create(ctx context.Context, rel *model.Release) (*model.Release, error) {
	social, attachments, err := releaseJSON(rel)
	if err != nil {
		return nil, err
	}

	row := r.db.QueryRow(ctx, `
		INSERT INTO public.kompu_releases
			(start_date, title, progress_status, social_media_status, sharepoint_folder_url,
			 attachment_urls, caption, created_by, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING `+releaseColumns,
		rel.StartDate, rel.Title, rel.ProgressStatus, social, rel.SharepointFolderURL,
		attachments, rel.Caption, rel.CreatedBy, rel.UpdatedBy)

	return scanRelease(row)
}

func (r *PostgresReleaseRepository) GetByID(ctx context.Context, id string) (*model.Release, error) {
	row := r.db.QueryRow(ctx,
		`SELECT `+releaseColumns+` FROM public.kompu_releases WHERE id = $1 AND deleted_at IS NULL`, id)

	rel, err := scanRelease(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, model.ErrNotFound
	}
	return rel, err
}

func (r *PostgresReleaseRepository) Update(ctx context.Context, rel *model.Release) (*model.Release, error) {
	social, attachments, err := releaseJSON(rel)
	if err != nil {
		return nil, err
	}

	row := r.db.QueryRow(ctx, `
		UPDATE public.kompu_releases
		SET start_date = $2, title = $3, progress_status = $4, social_media_status = $5,
		    sharepoint_folder_url = $6, attachment_urls = $7, caption = $8, updated_by = $9
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING `+releaseColumns,
		rel.ID, rel.StartDate, rel.Title, rel.ProgressStatus, social,
		rel.SharepointFolderURL, attachments, rel.Caption, rel.UpdatedBy)

	updated, err := scanRelease(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, model.ErrNotFound
	}
	return updated, err
}

// SoftDelete marks the release as deleted (the row is never removed).
func (r *PostgresReleaseRepository) SoftDelete(ctx context.Context, id, actor string) (time.Time, error) {
	var deletedAt time.Time
	err := r.db.QueryRow(ctx, `
		UPDATE public.kompu_releases
		SET deleted_at = now(), deleted_by = $2, updated_by = $2
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING deleted_at`, id, actor).Scan(&deletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, model.ErrNotFound
	}
	return deletedAt, err
}

// List returns one page of live releases (newest start date first) and the
// total number of live rows matching the filter.
func (r *PostgresReleaseRepository) List(ctx context.Context, f model.ReleaseFilter) ([]model.Release, int, error) {
	where, args := buildReleaseWhere(f)

	var total int
	if err := r.db.QueryRow(ctx,
		`SELECT count(*) FROM public.kompu_releases `+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count releases: %w", err)
	}

	items := []model.Release{}
	if total == 0 {
		return items, 0, nil
	}

	pageArgs := make([]any, 0, len(args)+2)
	pageArgs = append(pageArgs, args...)
	pageArgs = append(pageArgs, f.PageSize, (f.Page-1)*f.PageSize)

	query := fmt.Sprintf(
		`SELECT %s FROM public.kompu_releases %s
		 ORDER BY start_date DESC, created_at DESC, id
		 LIMIT $%d OFFSET $%d`,
		releaseColumns, where, len(args)+1, len(args)+2)

	rows, err := r.db.Query(ctx, query, pageArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("list releases: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		rel, err := scanRelease(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan release: %w", err)
		}
		items = append(items, *rel)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("list releases: %w", err)
	}
	return items, total, nil
}

// buildReleaseWhere turns a filter into a WHERE clause plus positional args.
// Soft-deleted rows are always excluded; user input never enters the SQL text.
func buildReleaseWhere(f model.ReleaseFilter) (string, []any) {
	conds := []string{"deleted_at IS NULL"}
	var args []any

	if f.Search != "" {
		args = append(args, "%"+escapeLike(f.Search)+"%")
		conds = append(conds, fmt.Sprintf("(title ILIKE $%[1]d OR caption ILIKE $%[1]d)", len(args)))
	}
	if f.Status != "" {
		args = append(args, f.Status)
		conds = append(conds, fmt.Sprintf("progress_status = $%d", len(args)))
	}
	if f.DateFrom != nil {
		args = append(args, *f.DateFrom)
		conds = append(conds, fmt.Sprintf("start_date >= $%d", len(args)))
	}
	if f.DateTo != nil {
		args = append(args, *f.DateTo)
		conds = append(conds, fmt.Sprintf("start_date <= $%d", len(args)))
	}

	return "WHERE " + strings.Join(conds, " AND "), args
}

func scanRelease(row rowScanner) (*model.Release, error) {
	var rel model.Release
	var social, attachments []byte

	if err := row.Scan(&rel.ID, &rel.StartDate, &rel.Title, &rel.ProgressStatus, &social,
		&rel.SharepointFolderURL, &attachments, &rel.Caption, &rel.CreatedAt, &rel.CreatedBy,
		&rel.UpdatedAt, &rel.UpdatedBy, &rel.DeletedAt, &rel.DeletedBy); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(social, &rel.SocialMediaStatus); err != nil {
		return nil, fmt.Errorf("decode social media status: %w", err)
	}
	if err := json.Unmarshal(attachments, &rel.Attachments); err != nil {
		return nil, fmt.Errorf("decode attachments: %w", err)
	}
	if rel.SocialMediaStatus == nil {
		rel.SocialMediaStatus = map[string]string{}
	}
	if rel.Attachments == nil {
		rel.Attachments = []model.EvidenceFile{}
	}
	return &rel, nil
}
