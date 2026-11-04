package metadata

import (
	"mime"
	"net/http"
	"path/filepath"
)

// detectMime prefers the name's registered type and falls back to sniffing the
// first bytes. The extension wins because sniffing gets text/plain for every
// source file, and because it keeps the answer stable for content that starts
// with the same bytes as something else.
func detectMime(name string, head []byte) string {
	if ext := filepath.Ext(name); ext != "" {
		if t := mime.TypeByExtension(ext); t != "" {
			return t
		}
	}
	if len(head) > 0 {
		return http.DetectContentType(head)
	}
	return "application/octet-stream"
}
