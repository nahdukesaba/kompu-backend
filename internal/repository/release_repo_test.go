package repository

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"kompu-backend/internal/model"
)

func TestBuildReleaseWhere(t *testing.T) {
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)
	search := "(title ILIKE $1 OR caption ILIKE $1)"

	tests := []struct {
		name     string
		filter   model.ReleaseFilter
		wantSQL  string
		wantArgs []any
	}{
		{
			name:    "no filters still hides soft-deleted rows",
			filter:  model.ReleaseFilter{},
			wantSQL: "WHERE deleted_at IS NULL",
		},
		{
			name:     "search only",
			filter:   model.ReleaseFilter{Search: "panen"},
			wantSQL:  "WHERE deleted_at IS NULL AND " + search,
			wantArgs: []any{"%panen%"},
		},
		{
			name:     "search escapes LIKE wildcards",
			filter:   model.ReleaseFilter{Search: "50%"},
			wantSQL:  "WHERE deleted_at IS NULL AND " + search,
			wantArgs: []any{`%50\%%`},
		},
		{
			name:     "status only",
			filter:   model.ReleaseFilter{Status: "review"},
			wantSQL:  "WHERE deleted_at IS NULL AND progress_status = $1",
			wantArgs: []any{"review"},
		},
		{
			name:     "date range only",
			filter:   model.ReleaseFilter{DateFrom: &from, DateTo: &to},
			wantSQL:  "WHERE deleted_at IS NULL AND start_date >= $1 AND start_date <= $2",
			wantArgs: []any{from, to},
		},
		{
			name:     "everything combined keeps placeholder numbering",
			filter:   model.ReleaseFilter{Search: "panen", Status: "draft", DateFrom: &from, DateTo: &to},
			wantSQL:  "WHERE deleted_at IS NULL AND " + search + " AND progress_status = $2 AND start_date >= $3 AND start_date <= $4",
			wantArgs: []any{"%panen%", "draft", from, to},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotSQL, gotArgs := buildReleaseWhere(tc.filter)
			assert.Equal(t, tc.wantSQL, gotSQL)
			assert.Equal(t, tc.wantArgs, gotArgs)
		})
	}
}
