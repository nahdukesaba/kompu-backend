package service

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"kompu-backend/internal/model"
)

const (
	dateLayout       = "2006-01-02"
	defaultPageSize  = 20
	maxPageSize      = 100
	maxSearchLen     = 100
	maxEvidenceFiles = 20 // also the limit for release attachments
	maxFileNameLen   = 255
)

var uuidPattern = regexp.MustCompile(
	`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// ValidationError carries every problem found in one request, so the client
// can fix them all at once. Handlers map it to HTTP 400.
type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	return "validation failed: " + strings.Join(e.Problems, "; ")
}

// problems collects validation messages.
type problems []string

func (p *problems) add(format string, args ...any) {
	*p = append(*p, fmt.Sprintf(format, args...))
}

func (p problems) err() error {
	if len(p) == 0 {
		return nil
	}
	return &ValidationError{Problems: p}
}

// parseOptionalDate parses an optional YYYY-MM-DD query value.
// Empty means "not provided" and returns nil.
func parseOptionalDate(field, value string, p *problems) *time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	t, err := time.Parse(dateLayout, value)
	if err != nil {
		p.add("%s must be in YYYY-MM-DD format", field)
		return nil
	}
	return &t
}

func normalizePaging(page, size int) (int, int) {
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = defaultPageSize
	}
	if size > maxPageSize {
		size = maxPageSize
	}
	return page, size
}

func totalPages(total, size int) int {
	if total <= 0 {
		return 0
	}
	return (total + size - 1) / size
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// normalizeFiles validates references to stored files (job evidence, release
// attachments). keyPattern must match exactly what the upload endpoint
// generates for the resource, so only files we issued can be attached.
func normalizeFiles(in []model.EvidenceFile, field, resource string, keyPattern *regexp.Regexp, p *problems) []model.EvidenceFile {
	out := make([]model.EvidenceFile, 0, len(in)) // never nil: stored as JSON []
	if len(in) > maxEvidenceFiles {
		p.add("%s must contain at most %d files", field, maxEvidenceFiles)
		return out
	}

	for i, f := range in {
		f.Key = strings.TrimSpace(f.Key)
		f.Name = strings.TrimSpace(f.Name)
		f.ContentType = strings.TrimSpace(f.ContentType)

		if !keyPattern.MatchString(f.Key) {
			p.add("%s[%d].key is not a valid %s upload key", field, i, resource)
		}
		if utf8.RuneCountInString(f.Name) > maxFileNameLen {
			p.add("%s[%d].name must be at most %d characters", field, i, maxFileNameLen)
		}
		if f.Size < 0 {
			p.add("%s[%d].size must not be negative", field, i)
		}
		out = append(out, f)
	}
	return out
}
