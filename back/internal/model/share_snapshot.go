package model

// ShareFileInfo is a single file in the sharing manifest.
type ShareFileInfo struct {
	Hash string `json:"hash"`
	Name string `json:"name"`
	Path string `json:"path,omitempty"` // Display path relative to the root directory (no absolute local path)
	Size int64  `json:"size"`
	Mime string `json:"mime,omitempty"`
}

// ShareEntryInfo is a file link for an entry within a collection.
type ShareEntryInfo struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
	Mime string `json:"mime,omitempty"`
}

// ShareCollectionInfo is a collection packaged for sharing.
type ShareCollectionInfo struct {
	Hash    string           `json:"hash"`
	Name    string           `json:"name,omitempty"`
	Size    int64            `json:"size,omitempty"` // Entry count (reuses the size name to stay consistent with the frontend card)
	Tags    []string         `json:"tags,omitempty"`
	Entries []ShareEntryInfo `json:"entries"`
}

// ShareSnapshot is the complete result of one share query.
type ShareSnapshot struct {
	Collections []ShareCollectionInfo `json:"collections"`
	Files       []ShareFileInfo       `json:"files"`
	Dirs        []string              `json:"dirs,omitempty"`
}
