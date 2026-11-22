package repository

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kompu-backend/internal/config"
	"kompu-backend/internal/database"
	"kompu-backend/internal/model"
)

// TestItemRepository_SQL runs every query against a real database inside one
// transaction that is always rolled back, so it leaves nothing behind.
func TestItemRepository_SQL(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := database.New(ctx, config.DatabaseConfig{URL: url, MaxConns: 2, MinConns: 1})
	require.NoError(t, err)
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck // the rollback is the point

	r := NewItemRepository(tx)
	const actor = "integration-test"
	const from, to = "2099-07-01", "2099-07-31"

	channels, err := r.Channels(ctx)
	require.NoError(t, err)
	codes := make([]string, 0, len(channels))
	for _, c := range channels {
		codes = append(codes, c.Code)
	}
	assert.Equal(t, []string{"instagram", "facebook", "website"}, codes)
	assert.True(t, channels[0].UsesFormat)
	assert.Equal(t, "Tanggal Rilis", channels[2].DateLabel)

	h2026, err := r.Holidays(ctx, "2026-01-01", "2026-12-31")
	require.NoError(t, err)
	assert.Len(t, h2026, 25)
	h2027, err := r.Holidays(ctx, "2027-01-01", "2027-12-31")
	require.NoError(t, err)
	assert.Len(t, h2027, 26)

	docs, err := r.Create(ctx, []*model.Item{{
		Kind: model.KindDocumentation, Title: "Rapat", StartDate: "2099-07-10", EndDate: "2099-07-14",
		People: []string{"Zz Test A", "Zz Test B"}, Location: "Medan", Objective: "Tujuan", ReportText: "Transkrip",
		Format: "feed", CreatedBy: actor,
	}})
	require.NoError(t, err)
	doc := docs[0]
	assert.Equal(t, "2099-07-14", doc.EndDate)
	assert.Empty(t, doc.ReportText, "lists skip the transcript")
	full, err := r.Get(ctx, doc.ID)
	require.NoError(t, err)
	assert.Equal(t, "Transkrip", full.ReportText)

	isDoc, err := r.IsDocumentation(ctx, doc.ID)
	require.NoError(t, err)
	assert.True(t, isDoc)

	pubs, err := r.Create(ctx, []*model.Item{
		{Kind: model.KindPublication, Title: "Post 1", StartDate: "2099-07-15", EndDate: "2099-07-15", People: []string{},
			Category: "kegiatan_balai", Status: "in_progress", Format: "reels", Caption: "c", DocumentationID: &doc.ID, CreatedBy: actor},
		{Kind: model.KindPublication, Title: "Post 2", StartDate: "2099-07-16", EndDate: "2099-07-16", People: []string{},
			Category: "harian_pusat", Status: "not_started", Format: "feed", CreatedBy: actor},
	})
	require.NoError(t, err)
	require.Len(t, pubs, 2)
	pub := pubs[0]
	require.NotNil(t, pub.DocumentationID)

	listed, err := r.List(ctx, "2099-07-14", "2099-07-15")
	require.NoError(t, err)
	assert.Len(t, listed, 2, "overlap: the 10-14 item and the 15 item")

	moved, err := r.Reschedule(ctx, pub.ID, "2099-07-20", "2099-07-22", actor)
	require.NoError(t, err)
	assert.Equal(t, "2099-07-22", moved.EndDate)

	require.NoError(t, r.SetChannel(ctx, pub.ID, "instagram", "2099-07-21", actor))
	require.NoError(t, r.SetChannel(ctx, pub.ID, "instagram", "2099-07-22", actor), "toggle again updates the date")
	require.ErrorIs(t, r.SetChannel(ctx, doc.ID, "instagram", "2099-07-21", actor), model.ErrNotFound,
		"documentation has no posting")

	ig := "instagram"
	const fileKey = "items/2099/07/0123456789abcdef0123456789abcdef.jpg"
	files := []model.ItemFile{{Slot: model.SlotPostProof, ChannelCode: &ig, Key: fileKey, Name: "a.jpg", ContentType: "image/jpeg", Size: 5}}
	require.NoError(t, r.AddFiles(ctx, pub.ID, actor, files))
	require.NoError(t, r.AddFiles(ctx, pub.ID, actor, files), "a retried request is ignored")
	got, err := r.Get(ctx, pub.ID)
	require.NoError(t, err)
	assert.Equal(t, "2099-07-22", got.Channels["instagram"])
	require.Len(t, got.Files, 1)
	assert.Equal(t, FilesRoute+fileKey, got.Files[0].URL)

	report, err := r.Report(ctx, from, to)
	require.NoError(t, err)
	assert.Len(t, report, 2, "the documentation and the posted publication, not the unposted one")

	people, err := r.People(ctx)
	require.NoError(t, err)
	assert.Contains(t, people, "Zz Test A")

	got.Title = "Post 1 edit"
	got.UpdatedBy = actor
	updated, err := r.Update(ctx, got)
	require.NoError(t, err)
	assert.Equal(t, "Post 1 edit", updated.Title)

	key, err := r.DeleteFile(ctx, pub.ID, got.Files[0].ID, actor)
	require.NoError(t, err)
	assert.Equal(t, fileKey, key)
	_, err = r.DeleteFile(ctx, pub.ID, got.Files[0].ID, actor)
	require.ErrorIs(t, err, model.ErrNotFound)

	require.NoError(t, r.UnsetChannel(ctx, pub.ID, "instagram", actor))
	got, err = r.Get(ctx, pub.ID)
	require.NoError(t, err)
	assert.Empty(t, got.Channels)

	require.NoError(t, r.SoftDelete(ctx, doc.ID, actor))
	_, err = r.Get(ctx, doc.ID)
	require.True(t, errors.Is(err, model.ErrNotFound))
	got, err = r.Get(ctx, pub.ID)
	require.NoError(t, err)
	assert.Nil(t, got.DocumentationID, "deleting documentation unlinks its publications")
	require.ErrorIs(t, r.SoftDelete(ctx, doc.ID, actor), model.ErrNotFound)
}
