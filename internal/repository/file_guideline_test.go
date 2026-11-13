package repository

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeGuideline(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "skills.md")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestFileGuideline_NoFileMeansNoGuideline(t *testing.T) {
	missing := NewFileGuideline(filepath.Join(t.TempDir(), "nope.md"))
	got, err := missing.Guideline(context.Background())
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.False(t, missing.Exists())

	empty := NewFileGuideline("")
	got, err = empty.Guideline(context.Background())
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestFileGuideline_ReadsAndCleansTheFile(t *testing.T) {
	g := NewFileGuideline(writeGuideline(t, "\ufeff# Panduan\r\n\r\n- Hangat dan sopan\r\n"))

	got, err := g.Guideline(context.Background())

	require.NoError(t, err)
	assert.Equal(t, "# Panduan\n\n- Hangat dan sopan", got)
	assert.True(t, g.Exists())
}

func TestFileGuideline_StripsFrontmatterOnly(t *testing.T) {
	content := "---\nname: caption-guideline\ndescription: x\n---\n# Panduan\n\nBagian satu\n\n---\n\nBagian dua\n"
	got, err := NewFileGuideline(writeGuideline(t, content)).Guideline(context.Background())

	require.NoError(t, err)
	assert.Equal(t, "# Panduan\n\nBagian satu\n\n---\n\nBagian dua", got,
		"a horizontal rule in the body must survive")
}

func TestFileGuideline_FrontmatterWithoutBody(t *testing.T) {
	got, err := NewFileGuideline(writeGuideline(t, "---\nname: x\n---")).Guideline(context.Background())
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestFileGuideline_PicksUpEditsWithoutARestart(t *testing.T) {
	path := writeGuideline(t, "versi satu")
	g := NewFileGuideline(path)

	first, err := g.Guideline(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "versi satu", first)

	require.NoError(t, os.WriteFile(path, []byte("versi dua"), 0o600))
	second, err := g.Guideline(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "versi dua", second)
}

func TestFileGuideline_RejectsOversizedAndDirectories(t *testing.T) {
	big := NewFileGuideline(writeGuideline(t, strings.Repeat("a", maxGuidelineBytes+1)))
	_, err := big.Guideline(context.Background())
	assert.Error(t, err)

	atLimit := NewFileGuideline(writeGuideline(t, strings.Repeat("a", maxGuidelineBytes)))
	_, err = atLimit.Guideline(context.Background())
	assert.NoError(t, err)

	_, err = NewFileGuideline(t.TempDir()).Guideline(context.Background())
	assert.Error(t, err)
}
