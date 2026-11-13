package model

import "errors"

// ErrNotFound is returned by repositories when a row does not exist.
var ErrNotFound = errors.New("resource not found")
