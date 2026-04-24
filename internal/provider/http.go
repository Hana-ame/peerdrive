// HTTP provider — fetches content from remote URLs via HTTP GET.
// Usage: registered with Manager as provider_type "http".

package provider

import (
	"io"
	"net/http"
	"path"
)

type HTTPProvider struct{}

func (p *HTTPProvider) GetReader(url string) (io.ReadCloser, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, err
	}
	return resp.Body, nil
}

func (p *HTTPProvider) GetFilenameHint(url, originalFilename string) string {
	if originalFilename != "" {
		return originalFilename
	}
	return path.Base(url)
}
