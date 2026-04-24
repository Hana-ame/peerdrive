package provider

import (
	"io"
)

type ContentProvider interface {
	GetReader(path string) (io.ReadCloser, error)
	GetFilenameHint(path, originalFilename string) string
}