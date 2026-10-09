// meta.go: video metadata + full resolution list for the iwara page.
//
// ResolveDownloadURL only ever returns the single best download URL, which is
// enough for a downloader but not for a page: the page needs the title, cover,
// author, duration and *every* resolution option (the user picks 1080p vs
// Source). This file adds the read-only metadata view on top of the same
// 2-request flow (video info → resolution list), reusing fetchVideoInfo /
// fetchResolutions instead of re-implementing the X-Version signing.
//
// Field-name caveat (discovery background): the API surface is probed through
// Cloudflare without the ech-proxy + login cookie, so the exact key names of
// author/cover/duration were not verified against a live response — they are
// taken from the open-source iwara downloaders' expectations and the frontend
// list of candidate keys below. Every field therefore decodes through a
// permissive "first candidate that is a string/number" lookup and is omitted
// from the response when the API does not return it, so a wrong guess costs a
// blank cell rather than a failing request.
package echproxy

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// VideoMeta is the JSON the iwara page renders: enough of the /video/{id}
// payload to fill the post body, plus every resolution's signed download URL.
type VideoMeta struct {
	ID          string                `json:"id"`
	Slug        string                `json:"slug"`
	Title       string                `json:"title"`
	Status      string                `json:"status"`
	Rating      string                `json:"rating"`
	Author      string                `json:"author,omitempty"`
	AuthorID    string                `json:"authorId,omitempty"`
	Cover       string                `json:"cover,omitempty"`
	Duration    int                   `json:"duration,omitempty"`
	Views       int                   `json:"views,omitempty"`
	Uploaded    int64                 `json:"uploaded,omitempty"`
	File        *VideoMetaFile        `json:"file,omitempty"`
	Resolutions []VideoMetaResolution `json:"resolutions,omitempty"`
}

// VideoMetaFile is the file descriptor embedded in the video payload.
type VideoMetaFile struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
}

// VideoMetaResolution is one download option: a resolution name ("Source",
// "1080p", ...) and the CDN URL the user downloads from.
type VideoMetaResolution struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DownloadURL string `json:"downloadUrl"`
}

// candidateKeys maps one semantic field to the spellings the iwara API has used
// across versions. The first key present in the payload wins.
var candidateKeys = map[string][]string{
	"title":    {"title", "name", "caption"},
	"slug":     {"slug", "url"},
	"status":   {"status"},
	"rating":   {"rating", "category"},
	"author":   {"author", "authorName", "uploader", "uploaderName", "username"},
	"authorId": {"authorId", "author_id", "userId", "user_id", "uploaderId"},
	"cover":    {"cover", "coverUrl", "cover_url", "thumbnail", "thumbnailUrl", "thumbnail_url", "poster", "preview"},
	"duration": {"duration", "durationSeconds", "duration_seconds", "length", "time"},
	"views":    {"views", "viewsAllTime", "views_all_time", "viewCount", "view_count"},
	"uploaded": {"uploaded", "createdAt", "created_at", "date", "published"},
}

// GetVideoMeta resolves a video ID (or iwara URL, via ParseVideoID) to the
// metadata payload + all resolution download URLs.
//
// Flow, same two API calls as ResolveDownloadURL:
//  1. GET /video/{id} → title/cover/author/duration/… + fileUrl
//  2. GET {fileUrl} with X-Version → every resolution + its CDN download URL
//
// Only the metadata is returned (no download happens here): the CDN URLs are
// handed back to the browser as-is, which is the same trust model as the
// existing ResolveDownloadURL.
func (c *IwaraClient) GetVideoMeta(ctx context.Context, videoID string) (*VideoMeta, error) {
	// Accept a bare video ID or a www.iwara.tv/videos/{id} URL, mirroring
	// ParseVideoID so the frontend can paste whatever it has. Anything else is
	// rejected before any network call — otherwise a pasted foreign URL would be
	// treated as a video ID and turned into GET /video/https://example.com/…
	// against the live API, which can only fail by timing out.
	id := strings.TrimSpace(videoID)
	if id == "" {
		return nil, fmt.Errorf("iwara: empty video ID")
	}
	parsed, ok := ParseVideoID(id)
	if !ok {
		return nil, fmt.Errorf("iwara: %q is not a video ID or iwara URL", videoID)
	}
	if parsed == "" {
		return nil, fmt.Errorf("iwara: empty video ID")
	}
	id = parsed

	info, raw, err := c.fetchVideoPayload(ctx, id)
	if err != nil {
		return nil, err
	}

	meta := &VideoMeta{
		ID:     info.ID,
		Title:  info.Title,
		Status: info.Status,
		Rating: info.Rating,
	}
	if info.File.ID != "" || info.File.Name != "" || info.File.Path != "" {
		meta.File = &VideoMetaFile{ID: info.File.ID, Name: info.File.Name, Path: info.File.Path}
	}
	// The raw payload carries the fields VideoInfo does not declare (author /
	// cover / duration / views). The first candidate key that is present wins;
	// a missing field stays empty and the frontend renders a placeholder.
	applyMetaCandidates(meta, raw)

	resolutions, err := c.fetchResolutions(ctx, info)
	if err != nil {
		return nil, err
	}
	for _, r := range resolutions {
		meta.Resolutions = append(meta.Resolutions, VideoMetaResolution{
			ID:          r.ID,
			Name:        r.Name,
			DownloadURL: r.DownloadURL,
		})
	}
	return meta, nil
}

// applyMetaCandidates fills meta from a decoded payload, tolerating missing or
// differently-typed fields (the API returns numbers as float64 via JSON, and
// omits whole objects for private/expired videos).
func applyMetaCandidates(meta *VideoMeta, raw map[string]any) {
	if raw == nil {
		return
	}
	str := func(keys []string) string {
		for _, k := range keys {
			if v, ok := raw[k].(string); ok && strings.TrimSpace(v) != "" {
				return v
			}
		}
		return ""
	}
	num := func(keys []string) float64 {
		for _, k := range keys {
			switch v := raw[k].(type) {
			case float64:
				return v
			case string:
				// Strict parse: a human-readable value like "2:30" must not be
				// silently truncated to 2 (a partial parse would render a wrong
				// duration the user would believe).
				if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
					return f
				}
			case bool: // some payloads encode counts as booleans; false → 0
				if v {
					return 1
				}
			}
		}
		return 0
	}

	if v := str(candidateKeys["title"]); v != "" {
		meta.Title = v
	}
	if v := str(candidateKeys["slug"]); v != "" {
		meta.Slug = v
	}
	if v := str(candidateKeys["status"]); v != "" {
		meta.Status = v
	}
	if v := str(candidateKeys["rating"]); v != "" {
		meta.Rating = v
	}
	if v := str(candidateKeys["author"]); v != "" {
		meta.Author = v
	}
	if v := str(candidateKeys["authorId"]); v != "" {
		meta.AuthorID = v
	}
	if v := str(candidateKeys["cover"]); v != "" {
		meta.Cover = v
	}
	if v := num(candidateKeys["duration"]); v != 0 {
		meta.Duration = int(v)
	}
	if v := num(candidateKeys["views"]); v != 0 {
		meta.Views = int(v)
	}
	if v := num(candidateKeys["uploaded"]); v != 0 {
		meta.Uploaded = int64(v)
	}
	if meta.ID == "" {
		if v := str([]string{"id"}); v != "" {
			meta.ID = v
		}
	}
}
