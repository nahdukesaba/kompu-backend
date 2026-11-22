package model

import "time"

// Item kinds: Kompu's work.
const (
	KindDocumentation = "documentation"
	KindPublication   = "publication"
	KindOther         = "other" // unpublished work such as staff cards and banners
)

// File slots.
const (
	SlotReport       = "report"        // activity report or meeting transcript
	SlotCaptureProof = "capture_proof" // proof the documentation was taken
	SlotResult       = "result"        // documentation result
	SlotPostProof    = "post_proof"    // screenshot of the published post, per channel
)

// Item is one tracked piece of work on the calendar. Dates are YYYY-MM-DD.
type Item struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind"`
	Title     string   `json:"title"`
	StartDate string   `json:"start_date"`
	EndDate   string   `json:"end_date"`
	People    []string `json:"people"`

	// Documentation.
	Location   string `json:"location"`
	Objective  string `json:"objective"`
	ReportText string `json:"report_text"`

	// Publication.
	Category        string  `json:"category"`
	Status          string  `json:"status"`
	Format          string  `json:"format"`
	Caption         string  `json:"caption"`
	DocumentationID *string `json:"documentation_id"`

	Channels map[string]string `json:"channels"` // channel code -> posted_on
	Files    []ItemFile        `json:"files"`

	CreatedAt time.Time `json:"created_at"`
	CreatedBy string    `json:"created_by"`
	UpdatedAt time.Time `json:"updated_at"`
	UpdatedBy string    `json:"updated_by"`
}

// ItemFile is one uploaded file; Key is its OneDrive object key.
type ItemFile struct {
	ID          string  `json:"id"`
	Slot        string  `json:"slot"`
	ChannelCode *string `json:"channel_code"`
	Key         string  `json:"key"`
	Name        string  `json:"name"`
	ContentType string  `json:"content_type"`
	Size        int64   `json:"size"`
	URL         string  `json:"url"`
}

// Channel is a social channel Kompu posts to.
type Channel struct {
	Code              string `json:"code"`
	Label             string `json:"label"`
	SortOrder         int    `json:"sort_order"`
	InQuarterlyReport bool   `json:"in_quarterly_report"`
	InMonthlyReport   bool   `json:"in_monthly_report"`
	UsesFormat        bool   `json:"uses_format"`
	DateLabel         string `json:"date_label"`
}

// Holiday is a national holiday (libur) or collective leave (cuti_bersama).
type Holiday struct {
	Date string `json:"date"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}
