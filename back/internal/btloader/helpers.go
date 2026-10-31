package btloader

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
)

// Completion is one download-complete event drained from the runtime.
type Completion struct {
	InfoHash string          `json:"infohash"`
	Files    []CompletedFile `json:"files"`
}

// maxBridgeReadBytes mirrors p2p_bt's maxBridgeFetchBytes (512 MiB) plus one
// byte of slack, so a file written by the runtime is readable but a wrong path
// pointing at something huge cannot be slurped into memory.
const maxBridgeReadBytes = 512<<20 + 1

func b64enc(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func b64dec(s string) ([]byte, error) {
	if s == "" {
		return nil, nil
	}
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("invalid base64 payload: %w", err)
	}
	return raw, nil
}

// readFileCapped reads a file the runtime wrote, refusing anything over the
// bridge limit.
func readFileCapped(path string, size int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read fetched file: %w", err)
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, maxBridgeReadBytes))
	if err != nil {
		return nil, fmt.Errorf("read fetched file: %w", err)
	}
	if int64(len(data)) > maxBridgeReadBytes-1 {
		return nil, fmt.Errorf("fetched file %s exceeds %d bytes", path, maxBridgeReadBytes-1)
	}
	if size > 0 && int64(len(data)) != size {
		return nil, fmt.Errorf("fetched file %s: size mismatch (runtime said %d, read %d)", path, size, len(data))
	}
	return data, nil
}

func isUnavailable(err error) bool { return errors.Is(err, ErrUnavailable) }
