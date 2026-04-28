package provider

import "io"

// Provider abstracts a file storage backend (local disk, HTTP, IPFS, etc.)
type Provider interface {
	GetReader(path string) (io.ReadCloser, int64, error)
	Name() string
}
