package service

import (
	"context"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"kompu-backend/internal/model"
)

const (
	maxReleaseTitleLen   = 200
	maxReleaseCaptionLen = 5000
	maxSharepointURLLen  = 2048
)

// ===== EDIT HERE: allowed values ============================================

// releaseProgressStatuses is the real-world status of a release:
//
//	not_started       work has not begun
//	in_progress       being worked on
//	paused            started, then put on hold
//	ready_to_release  work finished, waiting to be released
//	released          released to the public
//	cancelled         decided not to release it
var releaseProgressStatuses = map[string]bool{
	"not_started": true, "in_progress": true, "paused": true,
	"ready_to_release": true, "released": true, "cancelled": true,
}

// socialPlatforms maps a platform to its caption character limit. The limit is
// also given to the AI caption generator.
var socialPlatforms = map[string]int{
	"instagram": 2200, "facebook": 5000, "x": 280, "tiktok": 2200, "youtube": 5000, "website": 5000,
}

// Every platform always has exactly one of these: has the release been posted
// there in the real world?
const (
	socialNotPosted = "not_posted"
	socialPosted    = "posted"
)

var socialPostStatuses = map[string]bool{socialNotPosted: true, socialPosted: true}

// ============================================================================

// Matches exactly what the upload endpoint generates for the releases bucket.
var releaseAttachmentKeyPattern = regexp.MustCompile(`^releases/\d{4}/\d{2}/[0-9a-f]{32}\.[a-z0-9]{2,5}$`)

// ReleaseRepository is implemented by the persistence layer. Every method only
// sees live (not soft-deleted) rows.
type ReleaseRepository interface {
	Create(ctx context.Context, r *model.Release) (*model.Release, error)
	GetByID(ctx context.Context, id string) (*model.Release, error)
	Update(ctx context.Context, r *model.Release) (*model.Release, error)
	// SoftDelete marks the release deleted and returns the deletion time.
	SoftDelete(ctx context.Context, id, actor string) (time.Time, error)
	List(ctx context.Context, f model.ReleaseFilter) ([]model.Release, int, error)
}

// ReleaseInput is the raw, untrusted input for create/update.
type ReleaseInput struct {
	StartDate           string // YYYY-MM-DD
	Title               string
	ProgressStatus      string
	SocialMediaStatus   map[string]string
	SharepointFolderURL string
	Attachments         []model.EvidenceFile
	Caption             string
}

// ListReleasesQuery is the raw, untrusted input for listing.
type ListReleasesQuery struct {
	Search   string
	Status   string
	DateFrom string // YYYY-MM-DD, optional
	DateTo   string // YYYY-MM-DD, optional
	Page     int
	PageSize int
}

type ReleaseList struct {
	Items      []model.Release
	Page       int
	PageSize   int
	Total      int
	TotalPages int
}

type ReleaseService struct {
	repo ReleaseRepository
}

func NewReleaseService(repo ReleaseRepository) *ReleaseService {
	return &ReleaseService{repo: repo}
}

func (s *ReleaseService) Create(ctx context.Context, actor string, in ReleaseInput) (*model.Release, error) {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return nil, ErrMissingActor
	}
	rel, err := buildRelease(in)
	if err != nil {
		return nil, err
	}
	rel.CreatedBy, rel.UpdatedBy = actor, actor
	return s.repo.Create(ctx, rel)
}

func (s *ReleaseService) Get(ctx context.Context, id string) (*model.Release, error) {
	if !uuidPattern.MatchString(id) {
		return nil, model.ErrNotFound
	}
	rel, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	rel.SocialMediaStatus = completeSocialStatus(rel.SocialMediaStatus)
	return rel, nil
}

func (s *ReleaseService) Update(ctx context.Context, actor, id string, in ReleaseInput) (*model.Release, error) {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return nil, ErrMissingActor
	}
	if !uuidPattern.MatchString(id) {
		return nil, model.ErrNotFound
	}
	rel, err := buildRelease(in)
	if err != nil {
		return nil, err
	}
	rel.ID = id
	rel.UpdatedBy = actor
	return s.repo.Update(ctx, rel)
}

// Delete soft-deletes a release and returns when it happened. Attachments stay
// in storage.
func (s *ReleaseService) Delete(ctx context.Context, actor, id string) (time.Time, error) {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return time.Time{}, ErrMissingActor
	}
	if !uuidPattern.MatchString(id) {
		return time.Time{}, model.ErrNotFound
	}
	return s.repo.SoftDelete(ctx, id, actor)
}

func (s *ReleaseService) List(ctx context.Context, q ListReleasesQuery) (*ReleaseList, error) {
	var p problems

	search := strings.TrimSpace(q.Search)
	if utf8.RuneCountInString(search) > maxSearchLen {
		p.add("search must be at most %d characters", maxSearchLen)
	}
	status := strings.ToLower(strings.TrimSpace(q.Status))
	if status != "" && !releaseProgressStatuses[status] {
		p.add("status must be one of: %s", strings.Join(sortedKeys(releaseProgressStatuses), ", "))
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
	items, total, err := s.repo.List(ctx, model.ReleaseFilter{
		Search:   search,
		Status:   status,
		DateFrom: from,
		DateTo:   to,
		Page:     page,
		PageSize: size,
	})
	if err != nil {
		return nil, err
	}

	for i := range items {
		items[i].SocialMediaStatus = completeSocialStatus(items[i].SocialMediaStatus)
	}
	return &ReleaseList{
		Items:      items,
		Page:       page,
		PageSize:   size,
		Total:      total,
		TotalPages: totalPages(total, size),
	}, nil
}

// buildRelease validates and normalises input into a model.Release.
func buildRelease(in ReleaseInput) (*model.Release, error) {
	var p problems

	title := strings.TrimSpace(in.Title)
	caption := strings.TrimSpace(in.Caption)

	if title == "" {
		p.add("title is required")
	} else if utf8.RuneCountInString(title) > maxReleaseTitleLen {
		p.add("title must be at most %d characters", maxReleaseTitleLen)
	}
	if utf8.RuneCountInString(caption) > maxReleaseCaptionLen {
		p.add("caption must be at most %d characters", maxReleaseCaptionLen)
	}

	date, err := time.Parse(dateLayout, strings.TrimSpace(in.StartDate))
	if err != nil {
		p.add("start_date is required and must be in YYYY-MM-DD format")
	}

	status := strings.ToLower(strings.TrimSpace(in.ProgressStatus))
	if !releaseProgressStatuses[status] {
		p.add("progress_status must be one of: %s", strings.Join(sortedKeys(releaseProgressStatuses), ", "))
	}

	social := normalizeSocialStatus(in.SocialMediaStatus, &p)
	sharepoint := normalizeSharepointURL(in.SharepointFolderURL, &p)
	attachments := normalizeFiles(in.Attachments, "attachment_urls", "releases", releaseAttachmentKeyPattern, &p)

	if err := p.err(); err != nil {
		return nil, err
	}

	return &model.Release{
		StartDate:           date,
		Title:               title,
		ProgressStatus:      status,
		SocialMediaStatus:   social,
		SharepointFolderURL: sharepoint,
		Attachments:         attachments,
		Caption:             caption,
	}, nil
}

// normalizeSocialStatus lowercases and validates platform -> status pairs and
// fills in every platform that was not sent as not_posted.
func normalizeSocialStatus(in map[string]string, p *problems) map[string]string {
	out := make(map[string]string, len(in))
	for _, rawKey := range sortedKeys(in) {
		platform := strings.ToLower(strings.TrimSpace(rawKey))
		status := strings.ToLower(strings.TrimSpace(in[rawKey]))

		if _, ok := socialPlatforms[platform]; !ok {
			p.add("social_media_status has unknown platform %q (allowed: %s)",
				rawKey, strings.Join(sortedKeys(socialPlatforms), ", "))
			continue
		}
		if !socialPostStatuses[status] {
			p.add("social_media_status.%s must be one of: %s",
				platform, strings.Join(sortedKeys(socialPostStatuses), ", "))
			continue
		}
		out[platform] = status
	}
	return completeSocialStatus(out)
}

// completeSocialStatus guarantees every supported platform has a status, so the
// frontend can always render one checkbox per platform (this also covers rows
// saved before a platform was added). Missing platforms are not_posted.
// The result is a new map.
func completeSocialStatus(in map[string]string) map[string]string {
	out := make(map[string]string, len(socialPlatforms))
	for platform := range socialPlatforms {
		out[platform] = socialNotPosted
	}
	for platform, status := range in {
		out[platform] = status
	}
	return out
}

// normalizeSharepointURL accepts an empty value or a valid http(s) URL.
func normalizeSharepointURL(raw string, p *problems) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if len(raw) > maxSharepointURLLen {
		p.add("sharepoint_folder_url must be at most %d characters", maxSharepointURLLen)
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		p.add("sharepoint_folder_url must be a valid http(s) URL")
		return ""
	}
	return raw
}
