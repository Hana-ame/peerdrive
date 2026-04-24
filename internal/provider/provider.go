// Package provider defines the ContentProvider interface and pluggable providers.
// Supported provider types: "local" (local filesystem), "http" (remote URL).
// Usage: NewManager(localBaseDir) creates a Manager that routes GetReader calls based on provider_type.

package provider

import (
	"io"
)

type ContentProvider interface {
	GetReader(path string) (io.ReadCloser, error)
	GetFilenameHint(path, originalFilename string) string
}
