package repository

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"kompu-backend/internal/model"
)

func TestBuildJobWhere(t *testing.T) {
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)
	search := "(title ILIKE $1 OR objective ILIKE $1 OR location ILIKE $1 OR participants ILIKE $1)"

	tests := []struct {
		name     string
		filter   model.JobFilter
		wantSQL  string
		wantArgs []any
	}{
		{
			name:    "no filters still hides soft-deleted rows",
			filter:  model.JobFilter{},
			wantSQL: "WHERE deleted_at IS NULL",
		},
		{
			name:     "search only",
			filter:   model.JobFilter{Search: "rice"},
			wantSQL:  "WHERE deleted_at IS NULL AND " + search,
			wantArgs: []any{"%rice%"},
		},
		{
			name:     "search escapes LIKE wildcards",
			filter:   model.JobFilter{Search: `50%_\`},
			wantSQL:  "WHERE deleted_at IS NULL AND " + search,
			wantArgs: []any{`%50\%\_\\%`},
		},
		{
			name:     "date range only",
			filter:   model.JobFilter{DateFrom: &from, DateTo: &to},
			wantSQL:  "WHERE deleted_at IS NULL AND activity_date >= $1 AND activity_date <= $2",
			wantArgs: []any{from, to},
		},
		{
			name:     "only date_to",
			filter:   model.JobFilter{DateTo: &to},
			wantSQL:  "WHERE deleted_at IS NULL AND activity_date <= $1",
			wantArgs: []any{to},
		},
		{
			name:     "everything combined keeps placeholder numbering",
			filter:   model.JobFilter{Search: "rice", DateFrom: &from, DateTo: &to},
			wantSQL:  "WHERE deleted_at IS NULL AND " + search + " AND activity_date >= $2 AND activity_date <= $3",
			wantArgs: []any{"%rice%", from, to},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotSQL, gotArgs := buildJobWhere(tc.filter)
			assert.Equal(t, tc.wantSQL, gotSQL)
			assert.Equal(t, tc.wantArgs, gotArgs)
		})
	}
}

func TestEscapeLike(t *testing.T) {
	assert.Equal(t, "plain", escapeLike("plain"))
	assert.Equal(t, `100\%`, escapeLike("100%"))
	assert.Equal(t, `a\_b`, escapeLike("a_b"))
	assert.Equal(t, `a\\b`, escapeLike(`a\b`))
}
