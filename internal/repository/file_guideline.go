package repository

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
)

// maxGuidelineBytes keeps the prompt small: free model combos can have small
// context windows, and the whole file is sent with every caption request.
const maxGuidelineBytes = 16 * 1024

// FileGuideline serves the caption guideline (skills.md) from disk. The file is
// re-read on every call, so edits apply immediately without a restart.
type FileGuideline struct {
	path string
}

func NewFileGuideline(path string) *FileGuideline {
	return &FileGuideline{path: path}
}

func (f *FileGuideline) Path() string { return f.path }

// Exists reports whether the guideline file is present (used for the startup log).
func (f *FileGuideline) Exists() bool {
	if f.path == "" {
		return false
	}
	_, err := os.Stat(f.path)
	return err == nil
}

// Guideline returns the cleaned guideline text, or "" when no file exists.
// A file that exists but cannot be used (unreadable, a directory, too large)
// is an error, so the guideline is never silently ignored.
func (f *FileGuideline) Guideline(_ context.Context) (string, error) {
	if f.path == "" {
		return "", nil
	}

	file, err := os.Open(f.path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("open guideline: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("stat guideline: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("guideline path %q is a directory", f.path)
	}

	raw, err := io.ReadAll(io.LimitReader(file, maxGuidelineBytes+1))
	if err != nil {
		return "", fmt.Errorf("read guideline: %w", err)
	}
	if len(raw) > maxGuidelineBytes {
		return "", fmt.Errorf("guideline file is larger than the %d byte limit", maxGuidelineBytes)
	}
	return cleanGuideline(string(raw)), nil
}

// cleanGuideline normalises line endings, drops a BOM and removes an optional
// YAML frontmatter block (--- ... ---) from the top of the file.
func cleanGuideline(s string) string {
	s = strings.TrimPrefix(s, "\ufeff")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimSpace(s)

	if strings.HasPrefix(s, "---\n") {
		if end := strings.Index(s[4:], "\n---"); end >= 0 {
			rest := s[4+end+len("\n---"):]
			// drop the remainder of the closing delimiter line
			if nl := strings.Index(rest, "\n"); nl >= 0 {
				rest = rest[nl+1:]
			} else {
				rest = ""
			}
			s = strings.TrimSpace(rest)
		}
	}
	return s
}
