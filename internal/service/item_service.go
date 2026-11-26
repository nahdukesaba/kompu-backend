package service

import (
	"context"
	"log"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"kompu-backend/internal/model"
)

// ===== EDIT HERE: allowed values ============================================

var itemCategories = map[string]bool{
	"kegiatan_balai":    true, // activity of the Balai
	"ucapan_hari_besar": true, // holiday greeting
	"laporan":           true, // report about the Balai or the ministry
	"konten_kreatif":    true, // creative content
	"promosi_program":   true, // program promotion
	"harian_pusat":      true, // daily post required by Biro Kompu Pusat
}

var itemStatuses = map[string]bool{
	"not_started": true, "in_progress": true, "paused": true,
	"ready_to_release": true, "released": true, "cancelled": true,
}

var itemFormats = map[string]bool{"feed": true, "reels": true}

// ============================================================================

const (
	maxItemTitleLen     = 200
	maxItemLocationLen  = 300
	maxItemObjectiveLen = 3000
	maxItemCaptionLen   = 5000
	maxItemPeople       = 20
	maxPersonNameLen    = 100
	maxItemSpanDays     = 366
	maxSplitDays        = 62
	maxRangeDays        = 400
	maxFilesPerRequest  = 50
	maxResultLinkLen    = 2000
)

// wib is Medan's time zone; "today" for a post is the office's date.
var wib = time.FixedZone("WIB", 7*60*60)

// ItemStore is implemented by the persistence layer. Only live rows are visible.
type ItemStore interface {
	List(ctx context.Context, from, to string) ([]*model.Item, error)
	Report(ctx context.Context, from, to string) ([]*model.Item, error)
	Get(ctx context.Context, id string) (*model.Item, error)
	Create(ctx context.Context, items []*model.Item) ([]*model.Item, error)
	Update(ctx context.Context, it *model.Item) (*model.Item, error)
	Reschedule(ctx context.Context, id, start, end, actor string) (*model.Item, error)
	SetChannel(ctx context.Context, id, code, postedOn, actor string) error
	SetResultLink(ctx context.Context, id, link, actor string) error
	UnsetChannel(ctx context.Context, id, code, actor string) error
	AddFiles(ctx context.Context, id, actor string, files []model.ItemFile) error
	DeleteFile(ctx context.Context, id, fileID, actor string) (string, error)
	SoftDelete(ctx context.Context, id, actor string) error
	IsDocumentation(ctx context.Context, id string) (bool, error)
	Channels(ctx context.Context) ([]model.Channel, error)
	Holidays(ctx context.Context, from, to string) ([]model.Holiday, error)
	People(ctx context.Context) ([]string, error)
}

// FileRemover deletes stored objects and returns the keys it could not delete.
type FileRemover interface {
	DeleteFiles(ctx context.Context, keys []string) []string
}

type ItemService struct {
	store ItemStore
	files FileRemover
	now   func() time.Time
}

func NewItemService(store ItemStore, files FileRemover) *ItemService {
	return &ItemService{store: store, files: files, now: time.Now}
}

// ItemInput is the create/edit form. SplitDaily creates one item per day.
type ItemInput struct {
	Kind            string   `json:"kind"`
	Title           string   `json:"title"`
	StartDate       string   `json:"start_date"`
	EndDate         string   `json:"end_date"`
	People          []string `json:"people"`
	Location        string   `json:"location"`
	Objective       string   `json:"objective"`
	ReportText      string   `json:"report_text"`
	Category        string   `json:"category"`
	Status          string   `json:"status"`
	Format          string   `json:"format"`
	Caption         string   `json:"caption"`
	DocumentationID *string  `json:"documentation_id"`
	SplitDaily      bool     `json:"split_daily"`
}

// FileInput references a file already uploaded through a presigned URL.
type FileInput struct {
	Slot        string  `json:"slot"`
	ChannelCode *string `json:"channel_code"`
	Key         string  `json:"key"`
	Name        string  `json:"name"`
	ContentType string  `json:"content_type"`
	Size        int64   `json:"size"`
}

func (s *ItemService) List(ctx context.Context, from, to string) ([]*model.Item, error) {
	if err := validateRange(from, to); err != nil {
		return nil, err
	}
	return s.store.List(ctx, from, to)
}

func (s *ItemService) Report(ctx context.Context, from, to string) ([]*model.Item, error) {
	if err := validateRange(from, to); err != nil {
		return nil, err
	}
	return s.store.Report(ctx, from, to)
}

func (s *ItemService) Holidays(ctx context.Context, from, to string) ([]model.Holiday, error) {
	if err := validateRange(from, to); err != nil {
		return nil, err
	}
	return s.store.Holidays(ctx, from, to)
}

func (s *ItemService) Channels(ctx context.Context) ([]model.Channel, error) {
	return s.store.Channels(ctx)
}

func (s *ItemService) People(ctx context.Context) ([]string, error) { return s.store.People(ctx) }

func (s *ItemService) Get(ctx context.Context, id string) (*model.Item, error) {
	if !uuidPattern.MatchString(id) {
		return nil, model.ErrNotFound
	}
	return s.store.Get(ctx, id)
}

func (s *ItemService) Create(ctx context.Context, actor string, in ItemInput) ([]*model.Item, error) {
	it, err := s.normalize(ctx, in)
	if err != nil {
		return nil, err
	}
	it.CreatedBy, it.UpdatedBy = actor, actor
	if !in.SplitDaily || it.StartDate == it.EndDate {
		return s.store.Create(ctx, []*model.Item{it})
	}

	if it.Kind != model.KindPublication {
		return nil, &ValidationError{Problems: []string{"split_daily is only for publications"}}
	}
	start, _ := time.Parse(dateLayout, it.StartDate)
	end, _ := time.Parse(dateLayout, it.EndDate)
	days := int(end.Sub(start).Hours()/24) + 1
	if days > maxSplitDays {
		return nil, &ValidationError{Problems: []string{"split_daily covers at most 62 days"}}
	}
	items := make([]*model.Item, 0, days)
	for i := range days {
		day := *it
		day.StartDate = start.AddDate(0, 0, i).Format(dateLayout)
		day.EndDate = day.StartDate
		items = append(items, &day)
	}
	return s.store.Create(ctx, items)
}

func (s *ItemService) Update(ctx context.Context, actor, id string, in ItemInput) (*model.Item, error) {
	current, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	in.Kind = current.Kind // an item never changes kind
	it, err := s.normalize(ctx, in)
	if err != nil {
		return nil, err
	}
	if it.DocumentationID != nil && *it.DocumentationID == id {
		return nil, &ValidationError{Problems: []string{"an item cannot link to itself"}}
	}
	it.ID, it.UpdatedBy = id, actor
	return s.store.Update(ctx, it)
}

// Reschedule moves or resizes an item from the calendar.
func (s *ItemService) Reschedule(ctx context.Context, actor, id, start, end string) (*model.Item, error) {
	if !uuidPattern.MatchString(id) {
		return nil, model.ErrNotFound
	}
	var p problems
	validateSpan(start, end, &p)
	if err := p.err(); err != nil {
		return nil, err
	}
	return s.store.Reschedule(ctx, id, start, end, actor)
}

// SetChannel toggles a channel. postedOn defaults to today in WIB.
func (s *ItemService) SetChannel(ctx context.Context, actor, id, code string, posted bool, postedOn string) (*model.Item, error) {
	if !uuidPattern.MatchString(id) {
		return nil, model.ErrNotFound
	}
	if err := s.requireChannel(ctx, code); err != nil {
		return nil, err
	}
	if !posted {
		if err := s.store.UnsetChannel(ctx, id, code, actor); err != nil {
			return nil, err
		}
		return s.store.Get(ctx, id)
	}
	postedOn = strings.TrimSpace(postedOn)
	if postedOn == "" {
		postedOn = s.now().In(wib).Format(dateLayout)
	} else if _, err := time.Parse(dateLayout, postedOn); err != nil {
		return nil, &ValidationError{Problems: []string{"posted_on must be in YYYY-MM-DD format"}}
	}
	if err := s.store.SetChannel(ctx, id, code, postedOn, actor); err != nil {
		return nil, err
	}
	return s.store.Get(ctx, id)
}

// SetResultLink stores a SharePoint folder as a documentation's results. An
// empty link clears it, which allows uploads again.
func (s *ItemService) SetResultLink(ctx context.Context, actor, id, link string) (*model.Item, error) {
	current, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	link = strings.TrimSpace(link)
	var p problems
	if current.Kind != model.KindDocumentation {
		p.add("result_link is only for documentation")
	}
	if link != "" && !validSharePointLink(link) {
		p.add("result_link must be an https link to a sharepoint.com folder")
	}
	if link != "" && hasSlot(current.Files, model.SlotResult) {
		p.add("delete the uploaded results before saving a folder link")
	}
	if err := p.err(); err != nil {
		return nil, err
	}
	if err := s.store.SetResultLink(ctx, id, link, actor); err != nil {
		return nil, err
	}
	return s.store.Get(ctx, id)
}

func validSharePointLink(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || len(raw) > maxResultLinkLen {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "sharepoint.com" || strings.HasSuffix(host, ".sharepoint.com")
}

func hasSlot(files []model.ItemFile, slot string) bool {
	for _, f := range files {
		if f.Slot == slot {
			return true
		}
	}
	return false
}

func (s *ItemService) AddFiles(ctx context.Context, actor, id string, in []FileInput) (*model.Item, error) {
	current, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	var p problems
	if len(in) == 0 || len(in) > maxFilesPerRequest {
		p.add("files must contain 1 to %d files", maxFilesPerRequest)
	}
	files := make([]model.ItemFile, 0, len(in))
	for i, f := range in {
		f.Key, f.Name, f.ContentType = strings.TrimSpace(f.Key), strings.TrimSpace(f.Name), strings.TrimSpace(f.ContentType)
		if !model.ValidObjectKey(f.Key) {
			p.add("files[%d].key is not a valid upload key", i)
		}
		if f.Name == "" || utf8.RuneCountInString(f.Name) > maxFileNameLen {
			p.add("files[%d].name must be 1 to %d characters", i, maxFileNameLen)
		}
		if f.Size < 0 {
			p.add("files[%d].size must not be negative", i)
		}
		switch {
		case current.Kind == model.KindDocumentation && f.Slot == model.SlotResult && current.ResultLink != "":
			// shortcut: checked on the loaded item, not in SQL; two users racing a link and an upload can both win.
			p.add("files[%d]: remove the folder link before uploading results", i)
		case current.Kind == model.KindDocumentation &&
			(f.Slot == model.SlotReport || f.Slot == model.SlotCaptureProof || f.Slot == model.SlotResult):
			f.ChannelCode = nil
		case current.Kind == model.KindOther && f.Slot == model.SlotResult:
			f.ChannelCode = nil
		case current.Kind == model.KindPublication && f.Slot == model.SlotPostProof:
			if f.ChannelCode == nil {
				p.add("files[%d].channel_code is required for post_proof", i)
			} else if err := s.requireChannel(ctx, *f.ChannelCode); err != nil {
				return nil, err
			}
		default:
			p.add("files[%d].slot %q is not valid for a %s", i, f.Slot, current.Kind)
		}
		files = append(files, model.ItemFile{
			Slot: f.Slot, ChannelCode: f.ChannelCode, Key: f.Key,
			Name: f.Name, ContentType: f.ContentType, Size: f.Size,
		})
	}
	if err := p.err(); err != nil {
		return nil, err
	}
	if err := s.store.AddFiles(ctx, id, actor, files); err != nil {
		return nil, err
	}
	return s.store.Get(ctx, id)
}

// DeleteFile detaches the file, then moves it to the OneDrive recycle bin. A
// storage failure is logged: the file is already gone from the item.
func (s *ItemService) DeleteFile(ctx context.Context, actor, id, fileID string) (*model.Item, error) {
	if !uuidPattern.MatchString(id) || !uuidPattern.MatchString(fileID) {
		return nil, model.ErrNotFound
	}
	key, err := s.store.DeleteFile(ctx, id, fileID, actor)
	if err != nil {
		return nil, err
	}
	if failed := s.files.DeleteFiles(ctx, []string{key}); len(failed) > 0 {
		log.Printf("[items] file %s stays in storage after detaching from item %s", key, id)
	}
	return s.store.Get(ctx, id)
}

// Delete soft-deletes the item. Its files stay in OneDrive.
func (s *ItemService) Delete(ctx context.Context, actor, id string) error {
	if !uuidPattern.MatchString(id) {
		return model.ErrNotFound
	}
	return s.store.SoftDelete(ctx, id, actor)
}

func (s *ItemService) requireChannel(ctx context.Context, code string) error {
	channels, err := s.store.Channels(ctx)
	if err != nil {
		return err
	}
	for _, c := range channels {
		if c.Code == code {
			return nil
		}
	}
	return &ValidationError{Problems: []string{"unknown channel: " + code}}
}

// normalize validates the form and clears fields of the other kind.
func (s *ItemService) normalize(ctx context.Context, in ItemInput) (*model.Item, error) {
	var p problems
	it := &model.Item{
		Kind:      strings.TrimSpace(in.Kind),
		Title:     strings.TrimSpace(in.Title),
		StartDate: strings.TrimSpace(in.StartDate),
		EndDate:   strings.TrimSpace(in.EndDate),
		People:    normalizePeople(in.People, &p),
		Format:    "feed",
	}
	if it.Title == "" {
		p.add("title is required")
	} else if utf8.RuneCountInString(it.Title) > maxItemTitleLen {
		p.add("title must be at most %d characters", maxItemTitleLen)
	}
	validateSpan(it.StartDate, it.EndDate, &p)

	switch it.Kind {
	case model.KindDocumentation:
		it.Location = strings.TrimSpace(in.Location)
		it.Objective = strings.TrimSpace(in.Objective)
		it.ReportText = strings.TrimSpace(in.ReportText)
		if utf8.RuneCountInString(it.Location) > maxItemLocationLen {
			p.add("location must be at most %d characters", maxItemLocationLen)
		}
		if utf8.RuneCountInString(it.Objective) > maxItemObjectiveLen {
			p.add("objective must be at most %d characters", maxItemObjectiveLen)
		}
		if utf8.RuneCountInString(it.ReportText) > maxNotesLen {
			p.add("report_text must be at most %d characters", maxNotesLen)
		}
	case model.KindPublication:
		it.Category = strings.TrimSpace(in.Category)
		it.Status = strings.TrimSpace(in.Status)
		it.Caption = strings.TrimSpace(in.Caption)
		if it.Status == "" {
			it.Status = "not_started"
		}
		if f := strings.TrimSpace(in.Format); f != "" {
			it.Format = f
		}
		if !itemCategories[it.Category] {
			p.add("category must be one of: %s", strings.Join(sortedKeys(itemCategories), ", "))
		}
		if !itemStatuses[it.Status] {
			p.add("status must be one of: %s", strings.Join(sortedKeys(itemStatuses), ", "))
		}
		if !itemFormats[it.Format] {
			p.add("format must be one of: %s", strings.Join(sortedKeys(itemFormats), ", "))
		}
		if utf8.RuneCountInString(it.Caption) > maxItemCaptionLen {
			p.add("caption must be at most %d characters", maxItemCaptionLen)
		}
		if in.DocumentationID != nil && strings.TrimSpace(*in.DocumentationID) != "" {
			docID := strings.TrimSpace(*in.DocumentationID)
			if !uuidPattern.MatchString(docID) {
				p.add("documentation_id must be a UUID")
			} else {
				ok, err := s.store.IsDocumentation(ctx, docID)
				if err != nil {
					return nil, err
				}
				if !ok {
					p.add("documentation_id must point to a documentation item")
				}
				it.DocumentationID = &docID
			}
		}
	case model.KindOther:
		it.Status = strings.TrimSpace(in.Status)
		if it.Status == "" {
			it.Status = "not_started"
		}
		if !itemStatuses[it.Status] {
			p.add("status must be one of: %s", strings.Join(sortedKeys(itemStatuses), ", "))
		}
	default:
		p.add("kind must be documentation, publication, or other")
	}
	if err := p.err(); err != nil {
		return nil, err
	}
	return it, nil
}

func normalizePeople(in []string, p *problems) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, name := range in {
		name = strings.Join(strings.Fields(name), " ")
		key := strings.ToLower(name)
		if name == "" || seen[key] {
			continue
		}
		seen[key] = true
		if utf8.RuneCountInString(name) > maxPersonNameLen {
			p.add("person name must be at most %d characters", maxPersonNameLen)
		}
		out = append(out, name)
	}
	if len(out) > maxItemPeople {
		p.add("people must contain at most %d names", maxItemPeople)
	}
	return out
}

func validateSpan(start, end string, p *problems) {
	s, errS := time.Parse(dateLayout, start)
	e, errE := time.Parse(dateLayout, end)
	if errS != nil {
		p.add("start_date must be in YYYY-MM-DD format")
	}
	if errE != nil {
		p.add("end_date must be in YYYY-MM-DD format")
	}
	if errS != nil || errE != nil {
		return
	}
	if e.Before(s) {
		p.add("end_date must not be before start_date")
	} else if e.Sub(s) > maxItemSpanDays*24*time.Hour {
		p.add("an item can span at most %d days", maxItemSpanDays)
	}
}

func validateRange(from, to string) error {
	var p problems
	f, errF := time.Parse(dateLayout, from)
	t, errT := time.Parse(dateLayout, to)
	if errF != nil {
		p.add("from must be in YYYY-MM-DD format")
	}
	if errT != nil {
		p.add("to must be in YYYY-MM-DD format")
	}
	if errF == nil && errT == nil && (t.Before(f) || t.Sub(f) > maxRangeDays*24*time.Hour) {
		p.add("the date range must be 0 to %d days", maxRangeDays)
	}
	return p.err()
}
