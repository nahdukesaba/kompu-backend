package model

import "time"

// PresignPutInput describes one upload the storage backend should authorise.
type PresignPutInput struct {
	Bucket      string
	Key         string
	ContentType string
	Size        int64 // exact body size; signed into the URL
	Expires     time.Duration
}

// PresignedRequest is what the client must send to upload the object.
type PresignedRequest struct {
	URL     string
	Headers map[string]string
}
