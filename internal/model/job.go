package model

import "time"

// EvidenceFile points at an object in the jobs bucket.
type EvidenceFile struct {
	Key         string `json:"key"`
	Name        string `json:"name,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	Size        int64  `json:"size,omitempty"`
}

type Job struct {
	ID            string
	ActivityDate  time.Time // calendar date, held as UTC midnight
	Title         string
	Objective     string
	Location      string
	Participants  string
	EvidenceFiles []EvidenceFile
	CreatedAt     time.Time
	CreatedBy     string
	UpdatedAt     time.Time
	UpdatedBy     string
	DeletedAt     *time.Time
	DeletedBy     *string
}

// JobFilter is the validated, normalised input for listing jobs.
type JobFilter struct {
	Search   string
	DateFrom *time.Time
	DateTo   *time.Time
	Page     int
	PageSize int
}
