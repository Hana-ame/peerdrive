// Package source provides a unified file-fetching abstraction (the source system).
//
// Core idea: anything that can provide a "content-addressed byte stream" is a Source —
// local disk, p2p peers (passthrough), URL/HTTP (can go through ech-proxy and other egress),
// IPFS gateway. Upper layers (Manager consumers) only ask "give me the content for this hash",
// without caring about the source or network path.
//
// Capability flags: each source declares whether it supports full fetch (CapFile, Fetch
// returns []byte) or streaming/chunked reads (CapStream, Open supports offset/size).
// Manager routing selects the call mode by capability — large files must use CapStream
// (8GB full buffer would OOM, see transport streaming refactor).
//
// Routing semantics (Manager.Open): try in ascending priority; skip when Available()==false;
// return immediately on local hit (local authority for content addressing), fall back to
// peer/url on miss. All attempts are recorded in Stats (unified management plane, exposed
// via GET /sources).
//
// Dependency direction: this package depends on transport (FileIndexService/PeerJSService)
// and provider (IPFS); transport does not reverse-depend on this package — assembly is done
// in cmd/server/main.
package source

import (
	"context"
	"fmt"
	"io"
	"time"

	hashutil "peerdrive/pkg/hashutil"
)

// Capability source capability flag (flags, combinable).
type Capability uint8

const (
	// CapFile supports full file fetch (Fetch(ctx, hash) → []byte).
	// Sources without CapStream (e.g. HTTP endpoints that don't support Range) can only
	// do full fetch.
	CapFile Capability = 1 << iota
	// CapStream supports streaming/chunked reads (Open(ctx, hash, offset, size) → io.ReadCloser).
	// Large file routing must prefer CapStream sources — full buffer has memory limit risk.
	CapStream
)

// FileMeta file metadata returned by a source (Info optional capability, returns nil,nil if unsupported).
type FileMeta struct {
	Hash string
	Size int64
	Name string
	// Path is only meaningful for local sources (may be empty — peer/URL sources don't
	// expose local paths).
	Path string
}

// Source unified file-fetching source interface.
type Source interface {
	// Name unique identifier (registry key, duplicate registration rejected).
	Name() string
	// Type classification: local / peer / url / ipfs.
	Type() string
	// Capabilities declares capability flags (see Capability).
	Capabilities() Capability
	// Priority routing priority (smaller = tried first; Manager.SetPriority can adjust at runtime).
	Priority() int
	// SetPriority adjusts priority at runtime (one of the unified management capabilities).
	SetPriority(p int)
	// Available health check (soft state): when false, routing skips. Local = directory
	// readable, peer = has online connections, url = recent success / pingable.
	Available(ctx context.Context) bool
	// Open streaming open (requires CapStream). offset<0 normalizes to 0; size<0 means
	// to end of file. Full requests (offset==0 && size<0) must perform sha256 verification
	// (content-addressed fallback).
	Open(ctx context.Context, hash string, offset, size int64) (io.ReadCloser, error)
	// Fetch full fetch (requires CapFile). Full fetch must perform sha256 verification.
	Fetch(ctx context.Context, hash string) ([]byte, error)
	// Info metadata query (optional capability; returns nil, nil if unsupported).
	Info(ctx context.Context, hash string) (*FileMeta, error)
}

// IsStream checks if a source supports streaming chunks.
func IsStream(s Source) bool { return s.Capabilities()&CapStream != 0 }

// IsFile checks if a source supports full fetch.
func IsFile(s Source) bool { return s.Capabilities()&CapFile != 0 }

// Stats cumulative statistics per source (unified management data plane).
type Stats struct {
	Success int64     // success count
	Fail    int64     // failure count
	Bytes   int64     // cumulative transferred bytes
	LastErr string    // most recent failure reason (for multi-source troubleshooting)
	LastAt  time.Time // most recent attempt time
}

// SourceStatus management snapshot entry (GET /sources output).
type SourceStatus struct {
	Name         string     `json:"name"`
	Type         string     `json:"type"`
	Priority     int        `json:"priority"`
	Capabilities Capability `json:"capabilities"`
	Stream       bool       `json:"stream"` // convenience: whether streaming chunks are supported
	Available    bool       `json:"available"`
	Stats        Stats      `json:"stats"`
}

// Validates whether hash is a valid 64hex (unified defense at all source entry points).
func validHash(hash string) error {
	if !hashutil.IsStrictSHA256(hash) {
		return fmt.Errorf("invalid sha256 hash %q", hash)
	}
	return nil
}
