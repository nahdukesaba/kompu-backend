package service

import (
	"context"
	"errors"
	"log"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"kompu-backend/internal/model"
)

const (
	maxJobTitleLen     = 200
	maxJobObjectiveLen = 5000
	maxJobLocationLen  = 300
	maxParticipantsLen = 2000
)

// ErrMissingActor means a write was attempted without an authenticated user.
var ErrMissingActor = errors.New("authenticated user is required")

// Matches exactly what the Phase 4 upload endpoint generates for the jobs bucket.
var jobEvidenceKeyPattern = regexp.MustCompile(`^jobs/\d{4}/\d{2}/[0-9a-f]{32}\.[a-z0-9]{2,5}$`)

// JobRepository is implemented by the persistence layer. Every method only
// sees live (not soft-deleted) rows.
type JobRepository interface {
	Create(ctx context.Context, job *model.Job) (*model.Job, error)
	GetByID(ctx context.Context, id string) (*model.Job, error)
	Update(ctx context.Context, job *model.Job) (*model.Job, error)
	// SoftDelete marks the job deleted and returns it (including its files).
	SoftDelete(ctx context.Context, id, actor string) (*model.Job, error)
	// KeysReferencedByOthers returns those of keys that another live job also uses.
	KeysReferencedByOthers(ctx context.Context, excludeID string, keys []string) ([]string, error)
	List(ctx context.Context, filter model.JobFilter) ([]model.Job, int, error)
}

// FileStore removes stored files. Implemented by StorageService.
type FileStore interface {
	DeleteFiles(ctx context.Context, resource UploadResource, keys []string) (failed []string)
}

// JobInput is the raw, untrusted input for create/update.
type JobInput struct {
	ActivityDate  string // YYYY-MM-DD
	Title         string
	Objective     string
	Location      string
	Participants  string
	EvidenceFiles []model.EvidenceFile
}

// ListJobsQuery is the raw, untrusted input for listing.
type ListJobsQuery struct {
	Search   string
	DateFrom string // YYYY-MM-DD, optional
	DateTo   string // YYYY-MM-DD, optional
	Page     int
	PageSize int
}

type JobList struct {
	Items      []model.Job
	Page       int
	PageSize   int
	Total      int
	TotalPages int
}

type DeleteJobOptions struct {
	CleanupFiles bool // also permanently delete the job's files from storage
}

type DeleteJobResult struct {
	ID               string
	DeletedAt        time.Time
	CleanupRequested bool
	FilesDeleted     int
	FilesKeptShared  int // still used by another job, so left in place
	FilesFailed      int
}

type JobService struct {
	repo  JobRepository
	files FileStore
}

func NewJobService(repo JobRepository, files FileStore) *JobService {
	return &JobService{repo: repo, files: files}
}

func (s *JobService) Create(ctx context.Context, actor string, in JobInput) (*model.Job, error) {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return nil, ErrMissingActor
	}
	job, err := buildJob(in)
	if err != nil {
		return nil, err
	}
	job.CreatedBy, job.UpdatedBy = actor, actor
	return s.repo.Create(ctx, job)
}

func (s *JobService) Get(ctx context.Context, id string) (*model.Job, error) {
	if !uuidPattern.MatchString(id) {
		return nil, model.ErrNotFound
	}
	return s.repo.GetByID(ctx, id)
}

func (s *JobService) Update(ctx context.Context, actor, id string, in JobInput) (*model.Job, error) {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return nil, ErrMissingActor
	}
	if !uuidPattern.MatchString(id) {
		return nil, model.ErrNotFound
	}
	job, err := buildJob(in)
	if err != nil {
		return nil, err
	}
	job.ID = id
	job.UpdatedBy = actor
	return s.repo.Update(ctx, job)
}

// Delete soft-deletes a job. With opts.CleanupFiles it then permanently
// deletes the job's files, except files another live job still references.
// The soft delete is never rolled back if cleanup fails; failures are
// reported in the result instead.
func (s *JobService) Delete(ctx context.Context, actor, id string, opts DeleteJobOptions) (*DeleteJobResult, error) {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return nil, ErrMissingActor
	}
	if !uuidPattern.MatchString(id) {
		return nil, model.ErrNotFound
	}

	job, err := s.repo.SoftDelete(ctx, id, actor)
	if err != nil {
		return nil, err
	}

	res := &DeleteJobResult{ID: job.ID, CleanupRequested: opts.CleanupFiles}
	if job.DeletedAt != nil {
		res.DeletedAt = *job.DeletedAt
	}

	keys := uniqueKeys(job.EvidenceFiles)
	if !opts.CleanupFiles || len(keys) == 0 {
		return res, nil
	}

	shared, err := s.repo.KeysReferencedByOthers(ctx, job.ID, keys)
	if err != nil {
		// Never delete files we could not prove are unshared.
		log.Printf("[jobs] file cleanup skipped for job %s: %v", job.ID, err)
		res.FilesFailed = len(keys)
		return res, nil
	}

	sharedSet := make(map[string]bool, len(shared))
	for _, k := range shared {
		sharedSet[k] = true
	}
	toDelete := make([]string, 0, len(keys))
	for _, k := range keys {
		if !sharedSet[k] {
			toDelete = append(toDelete, k)
		}
	}

	var failed []string
	if len(toDelete) > 0 {
		failed = s.files.DeleteFiles(ctx, UploadResourceJobs, toDelete)
	}
	res.FilesKeptShared = len(keys) - len(toDelete)
	res.FilesFailed = len(failed)
	res.FilesDeleted = len(toDelete) - len(failed)
	return res, nil
}

func (s *JobService) List(ctx context.Context, q ListJobsQuery) (*JobList, error) {
	var p problems

	search := strings.TrimSpace(q.Search)
	if utf8.RuneCountInString(search) > maxSearchLen {
		p.add("search must be at most %d characters", maxSearchLen)
	}
	from := parseOptionalDate("date_from", q.DateFrom, &p)
	to := parseOptionalDate("date_to", q.DateTo, &p)
	if from != nil && to != nil && from.After(*to) {
		p.add("date_from must not be after date_to")
	}
	if err := p.err(); err != nil {
		return nil, err
	}

	page, size := normalizePaging(q.Page, q.PageSize)
	items, total, err := s.repo.List(ctx, model.JobFilter{
		Search:   search,
		DateFrom: from,
		DateTo:   to,
		Page:     page,
		PageSize: size,
	})
	if err != nil {
		return nil, err
	}

	return &JobList{
		Items:      items,
		Page:       page,
		PageSize:   size,
		Total:      total,
		TotalPages: totalPages(total, size),
	}, nil
}

// buildJob validates and normalises input into a model.Job.
func buildJob(in JobInput) (*model.Job, error) {
	var p problems

	title := strings.TrimSpace(in.Title)
	objective := strings.TrimSpace(in.Objective)
	location := strings.TrimSpace(in.Location)
	participants := strings.TrimSpace(in.Participants)

	if title == "" {
		p.add("title is required")
	} else if utf8.RuneCountInString(title) > maxJobTitleLen {
		p.add("title must be at most %d characters", maxJobTitleLen)
	}
	if utf8.RuneCountInString(objective) > maxJobObjectiveLen {
		p.add("objective must be at most %d characters", maxJobObjectiveLen)
	}
	if utf8.RuneCountInString(location) > maxJobLocationLen {
		p.add("location must be at most %d characters", maxJobLocationLen)
	}
	if utf8.RuneCountInString(participants) > maxParticipantsLen {
		p.add("participants must be at most %d characters", maxParticipantsLen)
	}

	date, err := time.Parse(dateLayout, strings.TrimSpace(in.ActivityDate))
	if err != nil {
		p.add("activity_date is required and must be in YYYY-MM-DD format")
	}

	files := normalizeFiles(in.EvidenceFiles, "evidence_files", "jobs", jobEvidenceKeyPattern, &p)

	if err := p.err(); err != nil {
		return nil, err
	}

	return &model.Job{
		ActivityDate:  date,
		Title:         title,
		Objective:     objective,
		Location:      location,
		Participants:  participants,
		EvidenceFiles: files,
	}, nil
}

func uniqueKeys(files []model.EvidenceFile) []string {
	seen := make(map[string]bool, len(files))
	keys := make([]string, 0, len(files))
	for _, f := range files {
		if f.Key != "" && !seen[f.Key] {
			seen[f.Key] = true
			keys = append(keys, f.Key)
		}
	}
	return keys
}
