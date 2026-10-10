// Package model defines the data structures for anonymous collections (AnonCollection) and their entries.
// Anonymous collections are stored via SHA256 content addressing, supporting version increments and tags.
// Version=1: old format entries[].hash (backward-compatible reading)
// Version=2: new format entries[].providers[] (sha256/url multi-source)
package model

import (
	"encoding/json"
	"time"
)

// Provider represents a file source, which can be a SHA256 hash or a URL.
type Provider struct {
	Type     string `json:"type"`                // "sha256" | "url"
	Value    string `json:"value"`               // 64-char hex hash, or HTTP(S) URL
	MimeType string `json:"mime_type,omitempty"` // Explicit MIME, overrides auto-detection
}

// AnonCollectionEntry represents a file entry in a collection, supporting multiple providers.
// Custom JSON handling: old format {path, hash} is automatically normalized to providers.
type AnonCollectionEntry struct {
	Path      string     `json:"path"`
	Providers []Provider `json:"providers"`
	Hash      string     `json:"hash,omitempty"` // Backward-compatible deserialization only
}

type anonCollectionEntryAlias AnonCollectionEntry

// UnmarshalJSON supports old format {path, hash} and new format {path, providers}.
func (e *AnonCollectionEntry) UnmarshalJSON(data []byte) error {
	var raw struct {
		Path      string     `json:"path"`
		Hash      string     `json:"hash,omitempty"`
		Providers []Provider `json:"providers,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	e.Path = raw.Path
	if len(raw.Providers) > 0 {
		e.Providers = raw.Providers
	} else if raw.Hash != "" {
		e.Providers = []Provider{{Type: "sha256", Value: raw.Hash}}
	} else {
		e.Providers = []Provider{}
	}
	return nil
}

// MarshalJSON outputs providers format; when Version=1, also outputs hash for old client compatibility.
func (e AnonCollectionEntry) MarshalJSON() ([]byte, error) {
	type out struct {
		Path      string     `json:"path"`
		Providers []Provider `json:"providers"`
		Hash      string     `json:"hash,omitempty"`
	}
	o := out{Path: e.Path, Providers: e.Providers}
	if len(e.Providers) > 0 {
		for _, p := range e.Providers {
			if p.Type == "sha256" && p.Value != "" {
				o.Hash = p.Value
				break
			}
		}
	}
	if o.Providers == nil {
		o.Providers = []Provider{}
	}
	return json.Marshal(o)
}

// Normalize migrates the Hash field set by struct literal to Providers (if not yet set).
func (e *AnonCollectionEntry) Normalize() {
	if len(e.Providers) == 0 && e.Hash != "" {
		e.Providers = []Provider{{Type: "sha256", Value: e.Hash}}
	}
}

// GetPrimaryHash returns the hash value of the first sha256 provider, or an empty string if none.
func (e *AnonCollectionEntry) GetPrimaryHash() string {
	for _, p := range e.Providers {
		if p.Type == "sha256" && p.Value != "" {
			return p.Value
		}
	}
	return ""
}

// GetPrimaryMime returns the explicit MIME or the first provider's MIME value.
func (e *AnonCollectionEntry) GetPrimaryMime() string {
	for _, p := range e.Providers {
		if p.MimeType != "" {
			return p.MimeType
		}
	}
	return ""
}

type AnonCollection struct {
	Version      int                   `json:"version"`
	FriendlyName string                `json:"friendly_name,omitempty"`
	Entries      []AnonCollectionEntry `json:"entries"`
	Tags         []string              `json:"tags,omitempty"`
	CreatedAt    string                `json:"created_at"`
	// Visibility: public=broadcastable / restricted=only AccessList accounts / private=only Owner.
	// Background: 2026-09 frontend "broadcast" changed to three options (public access / specific permissions only / only myself),
	// old anonymous collections had no permission field, empty values are treated as public for backward compatibility.
	Visibility string `json:"visibility,omitempty"`
	// AccessList is the account list allowed when restricted (unique username on the regserver side).
	AccessList []string `json:"access_list,omitempty"`
	// Owner is the publisher account (node operator), private collections only allow access to the Owner.
	Owner string `json:"owner,omitempty"`
	// AccessPolicy: public / protected (requires passcode) / private (owner only) (Issue #268).
	AccessPolicy string `json:"access_policy,omitempty"`
	// Passcode is the extraction key required to unlock entries in protected mode (Issue #268).
	Passcode string `json:"passcode,omitempty"`
	// IsProtected is true if entries are locked behind a passcode (Issue #268).
	IsProtected bool `json:"is_protected,omitempty"`
}

// visibility value constants: one-to-one mapping with the frontend three options, the backend only accepts these three strings.
const (
	VisibilityPublic     = "public"
	VisibilityRestricted = "restricted"
	VisibilityPrivate    = "private"
)

// Access policy constants (Issue #268).
const (
	AccessPolicyPublic    = "public"
	AccessPolicyProtected = "protected"
	AccessPolicyPrivate   = "private"
)

// IsValidVisibility validates visibility values; empty string is treated as unset (valid, equivalent to public).
func IsValidVisibility(v string) bool {
	switch v {
	case "", VisibilityPublic, VisibilityRestricted, VisibilityPrivate:
		return true
	}
	return false
}

// IsValidAccessPolicy validates access policy values; empty string is treated as unset (valid, equivalent to public).
func IsValidAccessPolicy(p string) bool {
	switch p {
	case "", AccessPolicyPublic, AccessPolicyProtected, AccessPolicyPrivate:
		return true
	}
	return false
}

// EffectiveVisibility returns the visibility after fallback: historical collections without this field → public.
func (c *AnonCollection) EffectiveVisibility() string {
	if c == nil || c.Visibility == "" {
		return VisibilityPublic
	}
	return c.Visibility
}

// EffectiveAccessPolicy returns the access policy after fallback (Issue #268).
func (c *AnonCollection) EffectiveAccessPolicy() string {
	if c == nil {
		return AccessPolicyPublic
	}
	if c.AccessPolicy != "" {
		return c.AccessPolicy
	}
	if c.Visibility == VisibilityPrivate {
		return AccessPolicyPrivate
	}
	if c.Passcode != "" {
		return AccessPolicyProtected
	}
	return AccessPolicyPublic
}

// CanView determines whether a requester (account name) can view the collection.
// Semantics: public allows everyone; restricted allows Owner + AccessList; private only allows Owner.
// Pitfall: empty requester represents an unauthenticated request; do not allow private/restricted based on this alone —
// otherwise any P2P sync without identity could pull restricted collections.
func (c *AnonCollection) CanView(requester string) bool {
	if c == nil {
		return false
	}
	switch c.EffectiveVisibility() {
	case VisibilityRestricted:
		if requester != "" && requester == c.Owner {
			return true
		}
		for _, a := range c.AccessList {
			if requester != "" && a == requester {
				return true
			}
		}
		return false
	case VisibilityPrivate:
		return requester != "" && requester == c.Owner
	default:
		return true
	}
}

// NormalizeEntries upgrades Version=1 old format entries to providers format.
func (c *AnonCollection) NormalizeEntries() {
	if c.Version >= 2 {
		return
	}
	for i := range c.Entries {
		if len(c.Entries[i].Providers) == 0 && c.Entries[i].Hash != "" {
			c.Entries[i].Providers = []Provider{{Type: "sha256", Value: c.Entries[i].Hash}}
		}
	}
	c.Version = 2
}

// NewAnonCollection creates a new anonymous collection, version 2 (providers format).
func NewAnonCollection(name string, entries []AnonCollectionEntry, tags []string) *AnonCollection {
	if entries == nil {
		entries = []AnonCollectionEntry{}
	}
	if tags == nil {
		tags = []string{}
	}
	return &AnonCollection{
		Version:      2,
		FriendlyName: name,
		Entries:      entries,
		Tags:         tags,
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
	}
}

type AnonCollectionSummary struct {
	Hash         string   `json:"hash"`
	FriendlyName string   `json:"friendly_name,omitempty"`
	NamePreview  string   `json:"name_preview,omitempty"`
	Version      int      `json:"version"`
	Tags         []string `json:"tags,omitempty"`
	EntryCount   int      `json:"entry_count"`
	CreatedAt    string   `json:"created_at"`
	// Visibility / Owner for the list page to directly display permission tier on collection cards (frontend three-option backfill).
	Visibility string `json:"visibility,omitempty"`
	Owner      string `json:"owner,omitempty"`
}
