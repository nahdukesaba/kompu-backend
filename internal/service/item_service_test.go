package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kompu-backend/internal/model"
)

const testItemID = "11111111-1111-4111-8111-111111111111"

type fakeItemStore struct {
	ItemStore
	item       *model.Item
	created    []*model.Item
	updated    *model.Item
	isDoc      bool
	gotPosted  string
	unset      bool
	addedFiles []model.ItemFile
}

func (f *fakeItemStore) Create(_ context.Context, items []*model.Item) ([]*model.Item, error) {
	f.created = items
	return items, nil
}

func (f *fakeItemStore) Get(context.Context, string) (*model.Item, error) {
	if f.item == nil {
		return nil, model.ErrNotFound
	}
	return f.item, nil
}

func (f *fakeItemStore) Update(_ context.Context, it *model.Item) (*model.Item, error) {
	f.updated = it
	return it, nil
}

func (f *fakeItemStore) IsDocumentation(context.Context, string) (bool, error) { return f.isDoc, nil }

func (f *fakeItemStore) Channels(context.Context) ([]model.Channel, error) {
	return []model.Channel{{Code: "instagram"}, {Code: "website"}}, nil
}

func (f *fakeItemStore) SetChannel(_ context.Context, _, _, postedOn, _ string) error {
	f.gotPosted = postedOn
	return nil
}

func (f *fakeItemStore) UnsetChannel(context.Context, string, string, string) error {
	f.unset = true
	return nil
}

func (f *fakeItemStore) SetResultLink(_ context.Context, _, link, _ string) error {
	f.item.ResultLink = link
	return nil
}

func (f *fakeItemStore) AddFiles(_ context.Context, _, _ string, files []model.ItemFile) error {
	f.addedFiles = files
	return nil
}

func publicationInput() ItemInput {
	return ItemInput{
		Kind: model.KindPublication, Title: "Hari Anak", StartDate: "2026-07-20", EndDate: "2026-07-23",
		Category: "ucapan_hari_besar", People: []string{" Sari  Dewi ", "sari dewi", "Budi"},
	}
}

func TestItemCreate_Validation(t *testing.T) {
	base := publicationInput()
	tests := []struct {
		name   string
		mutate func(*ItemInput)
		want   string
	}{
		{"kind", func(in *ItemInput) { in.Kind = "job" }, "kind must be"},
		{"title", func(in *ItemInput) { in.Title = "  " }, "title is required"},
		{"title length", func(in *ItemInput) { in.Title = strings.Repeat("a", 201) }, "title must be at most 200"},
		{"date format", func(in *ItemInput) { in.StartDate = "20-07-2026" }, "start_date must be"},
		{"date order", func(in *ItemInput) { in.EndDate = "2026-07-01" }, "end_date must not be before"},
		{"span", func(in *ItemInput) { in.EndDate = "2028-01-01" }, "at most 366 days"},
		{"category", func(in *ItemInput) { in.Category = "x" }, "category must be"},
		{"status", func(in *ItemInput) { in.Status = "x" }, "status must be"},
		{"format", func(in *ItemInput) { in.Format = "story" }, "format must be"},
		{"documentation link", func(in *ItemInput) { s := testItemID; in.DocumentationID = &s }, "must point to a documentation"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := base
			tc.mutate(&in)
			store := &fakeItemStore{}
			_, err := NewItemService(store, nil).Create(context.Background(), "u", in)
			var ve *ValidationError
			require.ErrorAs(t, err, &ve)
			assert.Contains(t, ve.Error(), tc.want)
			assert.Nil(t, store.created)
		})
	}
}

func TestItemCreate_NormalizesAndDefaults(t *testing.T) {
	store := &fakeItemStore{}
	in := publicationInput()
	in.Location = "must be cleared"
	_, err := NewItemService(store, nil).Create(context.Background(), "user-1", in)
	require.NoError(t, err)

	got := store.created[0]
	assert.Equal(t, []string{"Sari Dewi", "Budi"}, got.People, "spaces and duplicates are cleaned")
	assert.Equal(t, "not_started", got.Status)
	assert.Equal(t, "feed", got.Format)
	assert.Empty(t, got.Location, "documentation fields are cleared on a publication")
	assert.Equal(t, "user-1", got.CreatedBy)
}

func TestItemCreate_OtherKeepsOnlyGeneralFields(t *testing.T) {
	store := &fakeItemStore{}
	in := publicationInput()
	in.Kind = model.KindOther
	in.Status = "in_progress"
	in.Location, in.Caption = "must be cleared", "must be cleared"
	_, err := NewItemService(store, nil).Create(context.Background(), "u", in)
	require.NoError(t, err)

	got := store.created[0]
	assert.Equal(t, "in_progress", got.Status)
	assert.Empty(t, got.Category)
	assert.Empty(t, got.Location)
	assert.Empty(t, got.Caption)

	in.Status = "unknown"
	_, err = NewItemService(&fakeItemStore{}, nil).Create(context.Background(), "u", in)
	require.ErrorContains(t, err, "status must be")
}

func TestItemCreate_SplitDaily(t *testing.T) {
	store := &fakeItemStore{}
	in := publicationInput()
	in.StartDate, in.EndDate, in.SplitDaily = "2026-02-27", "2026-03-02", true
	items, err := NewItemService(store, nil).Create(context.Background(), "u", in)
	require.NoError(t, err)

	days := make([]string, 0, len(items))
	for _, it := range items {
		assert.Equal(t, it.StartDate, it.EndDate)
		days = append(days, it.StartDate)
	}
	assert.Equal(t, []string{"2026-02-27", "2026-02-28", "2026-03-01", "2026-03-02"}, days)

	in.Kind = model.KindDocumentation
	_, err = NewItemService(&fakeItemStore{}, nil).Create(context.Background(), "u", in)
	require.Error(t, err, "documentation cannot be split")

	in = publicationInput()
	in.StartDate, in.EndDate, in.SplitDaily = "2026-01-01", "2026-06-01", true
	_, err = NewItemService(&fakeItemStore{}, nil).Create(context.Background(), "u", in)
	require.ErrorContains(t, err, "at most 62 days")
}

func TestItemUpdate_KeepsKind(t *testing.T) {
	store := &fakeItemStore{item: &model.Item{ID: testItemID, Kind: model.KindDocumentation}}
	in := publicationInput()
	in.Location = "Medan"
	got, err := NewItemService(store, nil).Update(context.Background(), "u", testItemID, in)
	require.NoError(t, err)
	assert.Equal(t, model.KindDocumentation, got.Kind)
	assert.Equal(t, "Medan", got.Location)
	assert.Empty(t, got.Category)
}

func TestSetChannel_DefaultsToTodayInWIB(t *testing.T) {
	store := &fakeItemStore{item: &model.Item{ID: testItemID}}
	svc := NewItemService(store, nil)
	// 18:30 UTC on the 21st is already the 22nd in Medan.
	svc.now = func() time.Time { return time.Date(2026, 11, 21, 18, 30, 0, 0, time.UTC) }
	_, err := svc.SetChannel(context.Background(), "u", testItemID, "instagram", true, "")
	require.NoError(t, err)
	assert.Equal(t, "2026-11-22", store.gotPosted)

	_, err = svc.SetChannel(context.Background(), "u", testItemID, "instagram", true, "2026-11-20")
	require.NoError(t, err)
	assert.Equal(t, "2026-11-20", store.gotPosted)

	_, err = svc.SetChannel(context.Background(), "u", testItemID, "tiktok", true, "")
	require.ErrorContains(t, err, "unknown channel")

	_, err = svc.SetChannel(context.Background(), "u", testItemID, "instagram", false, "")
	require.NoError(t, err)
	assert.True(t, store.unset)
}

func TestAddFiles_SlotMustFitKind(t *testing.T) {
	key := "items/2026/11/0123456789abcdef0123456789abcdef.jpg"
	ig := "instagram"
	unknown := "tiktok"
	tests := []struct {
		name, kind string
		file       FileInput
		wantErr    string
	}{
		{"result on documentation", model.KindDocumentation, FileInput{Slot: model.SlotResult, Key: key, Name: "a.jpg"}, ""},
		{"proof on publication", model.KindPublication, FileInput{Slot: model.SlotPostProof, ChannelCode: &ig, Key: key, Name: "a.jpg"}, ""},
		{"result on publication", model.KindPublication, FileInput{Slot: model.SlotResult, Key: key, Name: "a.jpg"}, "not valid"},
		{"post proof on documentation", model.KindDocumentation, FileInput{Slot: model.SlotPostProof, ChannelCode: &ig, Key: key, Name: "a.jpg"}, "not valid"},
		{"result on other", model.KindOther, FileInput{Slot: model.SlotResult, Key: key, Name: "a.jpg"}, ""},
		{"post proof on other", model.KindOther, FileInput{Slot: model.SlotPostProof, ChannelCode: &ig, Key: key, Name: "a.jpg"}, "not valid"},
		{"proof without channel", model.KindPublication, FileInput{Slot: model.SlotPostProof, Key: key, Name: "a.jpg"}, "channel_code is required"},
		{"unknown channel", model.KindPublication, FileInput{Slot: model.SlotPostProof, ChannelCode: &unknown, Key: key, Name: "a.jpg"}, "unknown channel"},
		{"foreign key", model.KindDocumentation, FileInput{Slot: model.SlotResult, Key: "../secret.jpg", Name: "a.jpg"}, "not a valid upload key"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeItemStore{item: &model.Item{ID: testItemID, Kind: tc.kind}}
			_, err := NewItemService(store, nil).AddFiles(context.Background(), "u", testItemID, []FileInput{tc.file})
			if tc.wantErr == "" {
				require.NoError(t, err)
				assert.Len(t, store.addedFiles, 1)
				return
			}
			require.ErrorContains(t, err, tc.wantErr)
			assert.Nil(t, store.addedFiles)
		})
	}
}

func TestSetResultLink_OneResultSourcePerDocumentation(t *testing.T) {
	folder := "https://bp3kp.sharepoint.com/sites/humas/Shared%20Documents/2026/Rapat"
	result := model.ItemFile{Slot: model.SlotResult}
	tests := []struct {
		name, link, wantErr string
		item                model.Item
	}{
		{"saves a folder", folder, "", model.Item{Kind: model.KindDocumentation}},
		{"clears even with uploads", "", "", model.Item{Kind: model.KindDocumentation, Files: []model.ItemFile{result}}},
		{"refused after uploads", folder, "delete the uploaded results", model.Item{Kind: model.KindDocumentation, Files: []model.ItemFile{result}}},
		{"other files are fine", folder, "", model.Item{Kind: model.KindDocumentation, Files: []model.ItemFile{{Slot: model.SlotReport}}}},
		{"not sharepoint", "https://drive.google.com/x", "sharepoint.com", model.Item{Kind: model.KindDocumentation}},
		{"lookalike host", "https://sharepoint.com.evil.test/x", "sharepoint.com", model.Item{Kind: model.KindDocumentation}},
		{"no script links", "javascript:alert(1)//.sharepoint.com", "sharepoint.com", model.Item{Kind: model.KindDocumentation}},
		{"not documentation", folder, "only for documentation", model.Item{Kind: model.KindPublication}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.item.ID = testItemID
			store := &fakeItemStore{item: &tc.item}
			got, err := NewItemService(store, nil).SetResultLink(context.Background(), "u", testItemID, " "+tc.link+" ")
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.link, got.ResultLink)
		})
	}
}

func TestAddFiles_RefusesResultsWhenFolderLinked(t *testing.T) {
	store := &fakeItemStore{item: &model.Item{ID: testItemID, Kind: model.KindDocumentation, ResultLink: "https://x.sharepoint.com/f"}}
	key := "items/2026/11/0123456789abcdef0123456789abcdef.jpg"
	svc := NewItemService(store, nil)
	_, err := svc.AddFiles(context.Background(), "u", testItemID, []FileInput{{Slot: model.SlotResult, Key: key, Name: "a.jpg"}})
	require.ErrorContains(t, err, "remove the folder link")
	_, err = svc.AddFiles(context.Background(), "u", testItemID, []FileInput{{Slot: model.SlotCaptureProof, Key: key, Name: "a.jpg"}})
	require.NoError(t, err, "capture proof does not compete with the folder")
}

func TestRangeValidation(t *testing.T) {
	svc := NewItemService(&fakeItemStore{}, nil)
	for _, r := range [][2]string{{"", "2026-11-30"}, {"2026-11-30", "2026-11-01"}, {"2025-01-01", "2026-12-31"}} {
		_, err := svc.List(context.Background(), r[0], r[1])
		var ve *ValidationError
		require.ErrorAs(t, err, &ve, "%v", r)
	}
}
