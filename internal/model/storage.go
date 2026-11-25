package model

import "regexp"

// PresignPutInput describes one upload the storage backend should authorise.
type PresignPutInput struct {
	Key         string
	ContentType string
	Size        int64
}

// PresignedRequest is what the client must send to upload the object.
type PresignedRequest struct {
	URL     string
	Headers map[string]string
}

// objectKeyPattern matches the keys StorageService generates. The /files route
// and file attachments accept nothing else, so they cannot reach other
// OneDrive paths.
var objectKeyPattern = regexp.MustCompile(`^items/[0-9]{4}/[0-9]{2}/[0-9a-f]{32}\.(jpg|png|webp|pdf|mp4|mov|docx|txt)$`)

// ValidObjectKey reports whether key is a Kompu upload key.
func ValidObjectKey(key string) bool { return objectKeyPattern.MatchString(key) }
