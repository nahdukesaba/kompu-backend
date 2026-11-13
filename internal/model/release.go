package model

import "time"

type Release struct {
	ID                  string
	StartDate           time.Time // calendar date, held as UTC midnight
	Title               string
	ProgressStatus      string
	SocialMediaStatus   map[string]string // platform -> post status
	SharepointFolderURL string
	Attachments         []EvidenceFile // stored in the attachment_urls column as object keys
	Caption             string
	CreatedAt           time.Time
	CreatedBy           string
	UpdatedAt           time.Time
	UpdatedBy           string
	DeletedAt           *time.Time
	DeletedBy           *string
}

// ReleaseFilter is the validated, normalised input for listing releases.
type ReleaseFilter struct {
	Search   string
	Status   string
	DateFrom *time.Time
	DateTo   *time.Time
	Page     int
	PageSize int
}
