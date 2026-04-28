package model

// FileMeta holds metadata for a content-addressed file.
type FileMeta struct {
	Hash         string `json:"hash"`
	Filename     string `json:"filename"`
	Size         int64  `json:"size"`
	MimeType     string `json:"mime_type"`
	ProviderType string `json:"provider_type"`
	ProviderPath string `json:"provider_path,omitempty"`
	CreatedAt    string `json:"created_at"`
}

// FileProvider links a file hash to a storage provider.
type FileProvider struct {
	Hash         string `json:"hash"`
	ProviderType string `json:"provider_type"`
	ProviderPath string `json:"provider_path"`
	Available    bool   `json:"available"`
}
