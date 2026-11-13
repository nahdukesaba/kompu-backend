package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"kompu-backend/internal/model"
)

const jobColumns = `id::text, activity_date, title, objective, location, participants,
	evidence_files, created_at, created_by, updated_at, updated_by, deleted_at, deleted_by`

type PostgresJobRepository struct {
	db *pgxpool.Pool
}

func NewJobRepository(db *pgxpool.Pool) *PostgresJobRepository {
	return &PostgresJobRepository{db: db}
}

func (r *PostgresJobRepository) Create(ctx context.Context, j *model.Job) (*model.Job, error) {
	evidence, err := json.Marshal(j.EvidenceFiles)
	if err != nil {
		return nil, fmt.Errorf("marshal evidence files: %w", err)
	}

	row := r.db.QueryRow(ctx, `
		INSERT INTO public.kompu_jobs
			(activity_date, title, objective, location, participants, evidence_files, created_by, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING `+jobColumns,
		j.ActivityDate, j.Title, j.Objective, j.Location, j.Participants, evidence, j.CreatedBy, j.UpdatedBy)

	return scanJob(row)
}

func (r *PostgresJobRepository) GetByID(ctx context.Context, id string) (*model.Job, error) {
	row := r.db.QueryRow(ctx,
		`SELECT `+jobColumns+` FROM public.kompu_jobs WHERE id = $1 AND deleted_at IS NULL`, id)

	job, err := scanJob(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, model.ErrNotFound
	}
	return job, err
}

func (r *PostgresJobRepository) Update(ctx context.Context, j *model.Job) (*model.Job, error) {
	evidence, err := json.Marshal(j.EvidenceFiles)
	if err != nil {
		return nil, fmt.Errorf("marshal evidence files: %w", err)
	}

	row := r.db.QueryRow(ctx, `
		UPDATE public.kompu_jobs
		SET activity_date = $2, title = $3, objective = $4,
		    location = $5, participants = $6, evidence_files = $7, updated_by = $8
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING `+jobColumns,
		j.ID, j.ActivityDate, j.Title, j.Objective, j.Location, j.Participants, evidence, j.UpdatedBy)

	job, err := scanJob(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, model.ErrNotFound
	}
	return job, err
}

// SoftDelete marks the job as deleted (never removes the row) and returns it,
// including its evidence files, so the caller can clean them up.
func (r *PostgresJobRepository) SoftDelete(ctx context.Context, id, actor string) (*model.Job, error) {
	row := r.db.QueryRow(ctx, `
		UPDATE public.kompu_jobs
		SET deleted_at = now(), deleted_by = $2, updated_by = $2
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING `+jobColumns, id, actor)

	job, err := scanJob(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, model.ErrNotFound
	}
	return job, err
}

// KeysReferencedByOthers returns those of keys that another live job also lists
// in its evidence files.
func (r *PostgresJobRepository) KeysReferencedByOthers(ctx context.Context, excludeID string, keys []string) ([]string, error) {
	if len(keys) == 0 {
		return nil, nil
	}

	rows, err := r.db.Query(ctx, `
		SELECT DISTINCT f->>'key'
		FROM public.kompu_jobs j, jsonb_array_elements(j.evidence_files) AS f
		WHERE j.deleted_at IS NULL AND j.id <> $1 AND f->>'key' = ANY($2)`,
		excludeID, keys)
	if err != nil {
		return nil, fmt.Errorf("find shared files: %w", err)
	}
	defer rows.Close()

	var shared []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, fmt.Errorf("scan shared file: %w", err)
		}
		shared = append(shared, k)
	}
	return shared, rows.Err()
}

// List returns one page of live jobs (newest activity first) and the total
// number of live rows matching the filter.
func (r *PostgresJobRepository) List(ctx context.Context, f model.JobFilter) ([]model.Job, int, error) {
	where, args := buildJobWhere(f)

	var total int
	if err := r.db.QueryRow(ctx,
		`SELECT count(*) FROM public.kompu_jobs `+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count jobs: %w", err)
	}

	items := []model.Job{}
	if total == 0 {
		return items, 0, nil
	}

	pageArgs := make([]any, 0, len(args)+2)
	pageArgs = append(pageArgs, args...)
	pageArgs = append(pageArgs, f.PageSize, (f.Page-1)*f.PageSize)

	query := fmt.Sprintf(
		`SELECT %s FROM public.kompu_jobs %s
		 ORDER BY activity_date DESC, created_at DESC, id
		 LIMIT $%d OFFSET $%d`,
		jobColumns, where, len(args)+1, len(args)+2)

	rows, err := r.db.Query(ctx, query, pageArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("list jobs: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan job: %w", err)
		}
		items = append(items, *job)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("list jobs: %w", err)
	}
	return items, total, nil
}

// buildJobWhere turns a filter into a WHERE clause plus its positional args.
// Soft-deleted rows are always excluded. All user input goes through
// placeholders, never into the SQL text.
func buildJobWhere(f model.JobFilter) (string, []any) {
	conds := []string{"deleted_at IS NULL"}
	var args []any

	if f.Search != "" {
		args = append(args, "%"+escapeLike(f.Search)+"%")
		conds = append(conds, fmt.Sprintf(
			"(title ILIKE $%[1]d OR objective ILIKE $%[1]d OR location ILIKE $%[1]d OR participants ILIKE $%[1]d)",
			len(args)))
	}
	if f.DateFrom != nil {
		args = append(args, *f.DateFrom)
		conds = append(conds, fmt.Sprintf("activity_date >= $%d", len(args)))
	}
	if f.DateTo != nil {
		args = append(args, *f.DateTo)
		conds = append(conds, fmt.Sprintf("activity_date <= $%d", len(args)))
	}

	return "WHERE " + strings.Join(conds, " AND "), args
}

// escapeLike makes user text match literally inside ILIKE patterns.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// rowScanner is satisfied by both pgx.Row and pgx.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanJob(row rowScanner) (*model.Job, error) {
	var j model.Job
	var evidence []byte

	if err := row.Scan(&j.ID, &j.ActivityDate, &j.Title, &j.Objective, &j.Location,
		&j.Participants, &evidence, &j.CreatedAt, &j.CreatedBy, &j.UpdatedAt, &j.UpdatedBy,
		&j.DeletedAt, &j.DeletedBy); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(evidence, &j.EvidenceFiles); err != nil {
		return nil, fmt.Errorf("decode evidence files: %w", err)
	}
	if j.EvidenceFiles == nil {
		j.EvidenceFiles = []model.EvidenceFile{}
	}
	return &j, nil
}
