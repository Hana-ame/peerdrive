package service

import (
	"crypto/subtle"
	"fmt"
	"sort"
	"strings"

	"peerdrive/internal/log"
	"peerdrive/internal/model"
)

// CommitCollection creates a new version based on the source collection, supporting
// adding/updating/deleting entries, and auto-incrementing the version number.
func (s *AnonService) CommitCollection(
	sourceHash string,
	entries []model.AnonCollectionEntry,
	commitMessage string,
	requester string,
) (string, error) {
	return s.CommitCollectionWithPasscodeAndPSK(sourceHash, entries, commitMessage, "", "", requester)
}

// CommitCollectionWithPasscode commits modifications to an existing collection with optional passcode verification for protected collections.
func (s *AnonService) CommitCollectionWithPasscode(
	sourceHash string,
	entries []model.AnonCollectionEntry,
	commitMessage string,
	passcode string,
	requester string,
) (string, error) {
	return s.CommitCollectionWithPasscodeAndPSK(sourceHash, entries, commitMessage, passcode, "", requester)
}

// CommitCollectionWithPasscodeAndPSK commits modifications with passcode and PSK verification, setting prev_sha to parent version (Issue #338).
func (s *AnonService) CommitCollectionWithPasscodeAndPSK(
	sourceHash string,
	entries []model.AnonCollectionEntry,
	commitMessage string,
	passcode string,
	psk string,
	requester string,
) (string, error) {
	defer log.LogDuration("AnonService.CommitCollectionWithPasscodeAndPSK")()
	log.LogDebug("anon-svc: CommitCollection sourceHash=%s entries=%d", sourceHash, len(entries))

	src, err := s.GetCollectionByHash(sourceHash)
	if err != nil {
		log.LogError("anon-svc: CommitCollection source %s not found: %v", sourceHash, err)
		return "", fmt.Errorf("source collection not found: %w", err)
	}

	if !src.CanAccess(requester, psk) {
		if src.PSKHash != "" || src.EffectiveAccessPolicy() == model.AccessPolicyPSK || src.EffectiveVisibility() == model.VisibilityPSK {
			return "", fmt.Errorf("psk required or invalid for psk-protected collection")
		}
		return "", fmt.Errorf("source collection not found")
	}

	if src.EffectiveAccessPolicy() == model.AccessPolicyProtected && requester == "" {
		if subtle.ConstantTimeCompare([]byte(passcode), []byte(src.Passcode)) != 1 {
			return "", fmt.Errorf("passcode required or invalid for protected collection")
		}
	}

	// Normalize: struct literals / old formats may only set Hash without setting Providers
	for i := range entries {
		entries[i].Normalize()
	}

	for _, e := range entries {
		if e.Path == "" || !isRelativePath(e.Path) || strings.Contains(e.Path, "..") {
			err := fmt.Errorf("invalid path: %s", e.Path)
			log.LogError("anon-svc: CommitCollection invalid path: %s", e.Path)
			return "", err
		}
		if len(e.Providers) > 0 && !strings.HasSuffix(e.Path, "/") && !isValidProviders(e.Providers) {
			err := fmt.Errorf("invalid providers for path: %s", e.Path)
			log.LogError("anon-svc: CommitCollection invalid providers for path: %s", e.Path)
			return "", err
		}
	}

	// Merge source entries and new entries by providers
	entryMap := map[string][]model.Provider{}
	for _, e := range src.Entries {
		entryMap[e.Path] = e.Providers
	}
	for _, e := range entries {
		entryMap[e.Path] = e.Providers
	}

	sortedEntries := make([]model.AnonCollectionEntry, 0, len(entryMap))
	removeEmpty := false
	for path, providers := range entryMap {
		if len(providers) == 0 {
			removeEmpty = true
			continue
		}
		sortedEntries = append(sortedEntries, model.AnonCollectionEntry{Path: path, Providers: providers})
	}
	if removeEmpty {
		coll := newAnonCollectionWithVersion(src.FriendlyName, sortedEntries, src.Version+1, src.Tags)
		coll.PrevSHA = sourceHash
		inheritVisibility(coll, src)
		return s.saveCollectionJSON(coll)
	}

	sort.Slice(sortedEntries, func(i, j int) bool {
		return sortedEntries[i].Path < sortedEntries[j].Path
	})

	coll := newAnonCollectionWithVersion(src.FriendlyName, sortedEntries, src.Version+1, src.Tags)
	coll.PrevSHA = sourceHash
	inheritVisibility(coll, src)
	return s.saveCollectionJSON(coll)
}

// AddCollectionEntry adds or updates a single file entry in an existing collection, generating a new version with prev_sha pointing to sourceHash (Issue #338).
func (s *AnonService) AddCollectionEntry(sourceHash string, entry model.AnonCollectionEntry, requester, psk string) (string, error) {
	defer log.LogDuration("AnonService.AddCollectionEntry")()
	src, err := s.GetCollectionByHash(sourceHash)
	if err != nil {
		return "", fmt.Errorf("source collection not found: %w", err)
	}
	if !src.CanAccess(requester, psk) {
		if src.PSKHash != "" || src.EffectiveAccessPolicy() == model.AccessPolicyPSK || src.EffectiveVisibility() == model.VisibilityPSK {
			return "", fmt.Errorf("psk required or invalid for psk-protected collection")
		}
		return "", fmt.Errorf("source collection not found")
	}

	entry.Normalize()
	if entry.Path == "" || !isRelativePath(entry.Path) || strings.Contains(entry.Path, "..") {
		return "", fmt.Errorf("invalid path: %s", entry.Path)
	}
	if !strings.HasSuffix(entry.Path, "/") && !isValidProviders(entry.Providers) {
		return "", fmt.Errorf("invalid providers for path: %s", entry.Path)
	}

	found := false
	newEntries := make([]model.AnonCollectionEntry, len(src.Entries))
	for i, e := range src.Entries {
		if e.Path == entry.Path {
			newEntries[i] = entry
			found = true
		} else {
			newEntries[i] = e
		}
	}
	if !found {
		newEntries = append(newEntries, entry)
	}

	coll := newAnonCollectionWithVersion(src.FriendlyName, newEntries, src.Version+1, src.Tags)
	coll.PrevSHA = sourceHash
	inheritVisibility(coll, src)
	return s.saveCollectionJSON(coll)
}

// RemoveCollectionEntry removes an entry by path, generating a new version with prev_sha pointing to sourceHash (Issue #338).
func (s *AnonService) RemoveCollectionEntry(sourceHash, entryPath string, requester, psk string) (string, error) {
	defer log.LogDuration("AnonService.RemoveCollectionEntry")()
	src, err := s.GetCollectionByHash(sourceHash)
	if err != nil {
		return "", fmt.Errorf("source collection not found: %w", err)
	}
	if !src.CanAccess(requester, psk) {
		if src.PSKHash != "" || src.EffectiveAccessPolicy() == model.AccessPolicyPSK || src.EffectiveVisibility() == model.VisibilityPSK {
			return "", fmt.Errorf("psk required or invalid for psk-protected collection")
		}
		return "", fmt.Errorf("source collection not found")
	}

	found := false
	newEntries := make([]model.AnonCollectionEntry, 0, len(src.Entries))
	for _, e := range src.Entries {
		if e.Path == entryPath {
			found = true
			continue
		}
		newEntries = append(newEntries, e)
	}
	if !found {
		return "", fmt.Errorf("entry not found: %s", entryPath)
	}

	coll := newAnonCollectionWithVersion(src.FriendlyName, newEntries, src.Version+1, src.Tags)
	coll.PrevSHA = sourceHash
	inheritVisibility(coll, src)
	return s.saveCollectionJSON(coll)
}

// UpdateCollectionEntry updates an existing file entry in a collection, generating a new version with prev_sha pointing to sourceHash (Issue #338).
func (s *AnonService) UpdateCollectionEntry(sourceHash string, entry model.AnonCollectionEntry, requester, psk string) (string, error) {
	defer log.LogDuration("AnonService.UpdateCollectionEntry")()
	src, err := s.GetCollectionByHash(sourceHash)
	if err != nil {
		return "", fmt.Errorf("source collection not found: %w", err)
	}
	if !src.CanAccess(requester, psk) {
		if src.PSKHash != "" || src.EffectiveAccessPolicy() == model.AccessPolicyPSK || src.EffectiveVisibility() == model.VisibilityPSK {
			return "", fmt.Errorf("psk required or invalid for psk-protected collection")
		}
		return "", fmt.Errorf("source collection not found")
	}

	entry.Normalize()
	if entry.Path == "" || !isRelativePath(entry.Path) || strings.Contains(entry.Path, "..") {
		return "", fmt.Errorf("invalid path: %s", entry.Path)
	}
	if !strings.HasSuffix(entry.Path, "/") && !isValidProviders(entry.Providers) {
		return "", fmt.Errorf("invalid providers for path: %s", entry.Path)
	}

	found := false
	newEntries := make([]model.AnonCollectionEntry, len(src.Entries))
	for i, e := range src.Entries {
		if e.Path == entry.Path {
			newEntries[i] = entry
			found = true
		} else {
			newEntries[i] = e
		}
	}
	if !found {
		return "", fmt.Errorf("entry not found: %s", entry.Path)
	}

	coll := newAnonCollectionWithVersion(src.FriendlyName, newEntries, src.Version+1, src.Tags)
	coll.PrevSHA = sourceHash
	inheritVisibility(coll, src)
	return s.saveCollectionJSON(coll)
}

// GetCollectionHistory traverses backward along the prev_sha chain from startHash (Issue #338).
func (s *AnonService) GetCollectionHistory(startHash string, maxDepth int, requester, psk string) ([]*model.AnonCollectionSummary, error) {
	defer log.LogDuration("AnonService.GetCollectionHistory")()
	if !isValidHash(startHash) {
		return nil, fmt.Errorf("invalid start hash")
	}
	if maxDepth <= 0 || maxDepth > 200 {
		maxDepth = 50
	}

	visited := make(map[string]bool)
	currHash := startHash
	history := make([]*model.AnonCollectionSummary, 0)

	for currHash != "" && len(history) < maxDepth {
		if visited[currHash] {
			break // Cycle detection
		}
		visited[currHash] = true

		coll, err := s.GetCollectionByHash(currHash)
		if err != nil {
			break
		}
		if !coll.CanAccess(requester, psk) {
			break
		}

		preview := make([]string, 0, 3)
		for i, e := range coll.Entries {
			if i >= 3 {
				break
			}
			preview = append(preview, e.Path)
		}

		summary := &model.AnonCollectionSummary{
			Hash:         currHash,
			FriendlyName: coll.FriendlyName,
			NamePreview:  strings.Join(preview, ", "),
			Version:      coll.Version,
			Tags:         coll.Tags,
			EntryCount:   len(coll.Entries),
			CreatedAt:    coll.CreatedAt,
			Visibility:   coll.EffectiveVisibility(),
			Owner:        coll.Owner,
			PrevSHA:      coll.PrevSHA,
			AccessPolicy: coll.EffectiveAccessPolicy(),
			HasPSK:       coll.PSKHash != "",
		}
		history = append(history, summary)
		currHash = coll.PrevSHA
	}

	return history, nil
}
