// Package service provides the Peerdrive business logic layer, including anonymous collection management, download, P2P transfer, file registration, and other features.
package service

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"peerdrive/internal/config"
	"peerdrive/internal/log"
	"peerdrive/internal/model"
	"peerdrive/internal/repository"
)

type AnonService struct {
	config *config.Config
}

// NewAnonService creates a new anonymous collection service instance.
func NewAnonService(cfg *config.Config) *AnonService {
	return &AnonService{config: cfg}
}

func isRelativePath(p string) bool {
	return !filepath.IsAbs(p) && !strings.HasPrefix(p, "/")
}

var hashRe = regexp.MustCompile(`^[a-f0-9]{64}$`)

func isValidHash(h string) bool {
	return hashRe.MatchString(h)
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func isValidProviders(providers []model.Provider) bool {
	if len(providers) == 0 {
		return false
	}
	hasValid := false
	for _, p := range providers {
		if p.Type == "" && p.Value == "" {
			continue
		}
		hasValid = true
		switch p.Type {
		case "sha256":
			if !isValidHash(p.Value) {
				return false
			}
		case "url":
			u, err := url.ParseRequestURI(p.Value)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
				return false
			}
		default:
			return false
		}
	}
	return hasValid
}

// CreateCollection creates an anonymous collection (defaults to public, compatible with legacy callers/tests).
func (s *AnonService) CreateCollection(name string, entries []model.AnonCollectionEntry, tags []string) (string, error) {
	return s.CreateCollectionWithVisibility(name, entries, tags, model.VisibilityPublic, nil, "")
}

// CreateCollectionWithVisibility creates an anonymous collection with visibility settings.
func (s *AnonService) CreateCollectionWithVisibility(
	name string,
	entries []model.AnonCollectionEntry,
	tags []string,
	visibility string,
	accessList []string,
	owner string,
) (string, error) {
	return s.CreateCollectionWithPolicy(name, entries, tags, visibility, accessList, "", "", owner)
}

// CreateCollectionWithPolicy creates an anonymous collection with visibility and access policy settings (Issue #268).
func (s *AnonService) CreateCollectionWithPolicy(
	name string,
	entries []model.AnonCollectionEntry,
	tags []string,
	visibility string,
	accessList []string,
	accessPolicy string,
	passcode string,
	owner string,
) (string, error) {
	return s.CreateCollectionWithPolicyAndPSK(name, entries, tags, visibility, accessList, accessPolicy, passcode, "", owner)
}

// CreateCollectionWithPolicyAndPSK creates an anonymous collection with visibility, access policy, and PSK key settings (Issue #338).
func (s *AnonService) CreateCollectionWithPolicyAndPSK(
	name string,
	entries []model.AnonCollectionEntry,
	tags []string,
	visibility string,
	accessList []string,
	accessPolicy string,
	passcode string,
	psk string,
	owner string,
) (string, error) {
	defer log.LogDuration("AnonService.CreateCollectionWithPolicyAndPSK")()
	log.LogDebug("anon-svc: CreateCollectionWithPolicyAndPSK name=%s entries=%d visibility=%s policy=%s owner=%s",
		name, len(entries), visibility, accessPolicy, owner)

	if !model.IsValidVisibility(visibility) {
		err := fmt.Errorf("invalid visibility: %s", visibility)
		log.LogError("anon-svc: %v", err)
		return "", err
	}
	if !model.IsValidAccessPolicy(accessPolicy) {
		err := fmt.Errorf("invalid access policy: %s", accessPolicy)
		log.LogError("anon-svc: %v", err)
		return "", err
	}
	if accessPolicy == model.AccessPolicyProtected && passcode == "" {
		err := fmt.Errorf("passcode required for protected access policy")
		log.LogError("anon-svc: %v", err)
		return "", err
	}
	if (accessPolicy == model.AccessPolicyPSK || visibility == model.VisibilityPSK) && psk == "" {
		err := fmt.Errorf("psk required for psk access policy")
		log.LogError("anon-svc: %v", err)
		return "", err
	}
	// restricted must have an allowlist: otherwise nobody except the owner can see this
	// collection, which is equivalent to mistakenly setting it to private
	if visibility == model.VisibilityRestricted && len(accessList) == 0 {
		err := fmt.Errorf("access_list required for restricted visibility")
		log.LogError("anon-svc: %v", err)
		return "", err
	}

	// Normalize: struct literals may set Hash directly but forget to set Providers
	for i := range entries {
		entries[i].Normalize()
	}

	for _, e := range entries {
		if e.Path == "" || !isRelativePath(e.Path) || strings.Contains(e.Path, "..") {
			err := fmt.Errorf("invalid path: %s", e.Path)
			log.LogError("anon-svc: CreateCollectionWithVisibility invalid path: %s", e.Path)
			return "", err
		}
		if !strings.HasSuffix(e.Path, "/") && !isValidProviders(e.Providers) {
			err := fmt.Errorf("invalid providers for path: %s", e.Path)
			log.LogError("anon-svc: CreateCollectionWithVisibility invalid providers for path: %s", e.Path)
			return "", err
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Path < entries[j].Path
	})

	coll := model.NewAnonCollection(name, entries, tags)
	coll.Visibility = visibility
	coll.AccessList = accessList
	coll.AccessPolicy = accessPolicy
	coll.Passcode = passcode
	if psk != "" {
		if len(psk) == 64 && isValidHash(psk) {
			coll.PSKHash = psk
		} else {
			coll.PSKHash = sha256Hex([]byte(psk))
		}
		coll.IsProtected = true
	}
	coll.Owner = owner
	jsonBytes, err := json.MarshalIndent(coll, "", "  ")
	if err != nil {
		log.LogError("anon-svc: CreateCollectionWithVisibility marshal failed: %v", err)
		return "", err
	}

	hash := sha256Hex(jsonBytes)

	targetDir := filepath.Join(s.config.StorageDir, hash[:2])
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		log.LogError("anon-svc: CreateCollection mkdir failed: %v", err)
		return "", err
	}
	targetPath := filepath.Join(targetDir, hash)
	if err := os.WriteFile(targetPath, jsonBytes, 0644); err != nil {
		log.LogError("anon-svc: CreateCollection write failed: %v", err)
		return "", err
	}

	relPath := filepath.Join(hash[:2], hash)
	_ = repository.InsertFileMeta(&model.FileMeta{
		Hash:     hash,
		Size:     int64(len(jsonBytes)),
		MimeType: "application/json",
		Filename: "anon_collection.json",
		Gziped:   false,
		Type:     repository.FileTypeAnonCollection,
	})
	_ = repository.InsertFileProvider(hash, "local", relPath)

	log.LogInfo("anon-svc: CreateCollection %s -> hash=%s", name, hash)
	return hash, nil
}

// GetCollectionByHash reads an anonymous collection from content-addressed storage by SHA256 hash.
func (s *AnonService) GetCollectionByHash(hash string) (*model.AnonCollection, error) {
	defer log.LogDuration("AnonService.GetCollectionByHash")()
	log.LogDebug("anon-svc: GetCollectionByHash hash=%s", hash)

	// Defense: hash comes from URL path/request body/remote P2P sync. Without length
	// validation, hash[:2] will panic with slice out of bounds; non-64-digit hex values
	// (like "..") will also let filepath.Join escape the storage directory.
	// Invalid hashes return not-found directly.
	if !isValidHash(hash) {
		log.LogWarn("anon-svc: GetCollectionByHash invalid hash=%q", hash)
		return nil, fmt.Errorf("collection not found")
	}

	filePath := filepath.Join(s.config.StorageDir, hash[:2], hash)
	data, err := os.ReadFile(filePath)
	if err != nil {
		log.LogError("anon-svc: GetCollectionByHash %s not found: %v", hash, err)
		return nil, fmt.Errorf("collection not found")
	}
	var coll model.AnonCollection
	if err := json.Unmarshal(data, &coll); err != nil {
		log.LogError("anon-svc: GetCollectionByHash %s invalid JSON: %v", hash, err)
		return nil, fmt.Errorf("invalid collection json")
	}
	if coll.Version < 1 {
		log.LogError("anon-svc: GetCollectionByHash %s unsupported version: %d", hash, coll.Version)
		return nil, fmt.Errorf("unsupported version: %d", coll.Version)
	}
	coll.NormalizeEntries()
	log.LogInfo("anon-svc: GetCollectionByHash %s found (version=%d, entries=%d)", hash, coll.Version, len(coll.Entries))
	return &coll, nil
}

// ListCollections returns a summary list of all registered anonymous collections.
func (s *AnonService) ListCollections() ([]model.AnonCollectionSummary, error) {
	return repository.ListAnonCollections(s.config.StorageDir)
}

// GetCollectionVisibleTo returns a collection to the requester based on visibility:
// unauthorized access is equivalent to not existing (404 semantics).
// requester is the account bound to the current request; local WS sessions are passed
// the operator from the controller, so the operator can always see their own
// private/restricted collections.
// Pitfall: do NOT allow access just because "no account is available" --
// unauthenticated users can only see public collections.
func (s *AnonService) GetCollectionVisibleTo(hash, requester string) (*model.AnonCollection, error) {
	coll, err := s.GetCollectionByHash(hash)
	if err != nil {
		return nil, err
	}
	if !coll.CanView(requester) {
		log.LogWarn("anon-svc: GetCollectionVisibleTo denied hash=%s requester=%q visibility=%s",
			hash, requester, coll.EffectiveVisibility())
		return nil, fmt.Errorf("collection not found")
	}
	return coll, nil
}

// UpdateCollectionVisibility creates a new collection with changed permissions based on the original.
// Background: the frontend changed "broadcast" to a three-tier switch, so users can
// switch tiers at any time. Collections are content-addressed, and permissions are
// written into the JSON and participate in the digest, so changing tiers necessarily
// produces a new hash -- the return value is the new identity.
// Access control: only the Owner (or a legacy local collection without an Owner) can change this.
func (s *AnonService) UpdateCollectionVisibility(hash, visibility string, accessList []string, requester string) (string, error) {
	defer log.LogDuration("AnonService.UpdateCollectionVisibility")()
	if !model.IsValidVisibility(visibility) {
		return "", fmt.Errorf("invalid visibility: %s", visibility)
	}
	if visibility == model.VisibilityRestricted && len(accessList) == 0 {
		return "", fmt.Errorf("access_list required for restricted visibility")
	}

	src, err := s.GetCollectionByHash(hash)
	if err != nil {
		return "", err
	}
	// Legacy collections without an Owner (pre-dating the permissions fields) are treated
	// as local collections and allowed; once an Owner exists it must match, to prevent
	// a third party who obtained the hash from rewriting someone else's permission tier.
	if src.Owner != "" && src.Owner != requester {
		log.LogWarn("anon-svc: UpdateCollectionVisibility denied hash=%s owner=%s requester=%q", hash, src.Owner, requester)
		return "", fmt.Errorf("collection not found")
	}

	dst := *src
	dst.PrevSHA = hash
	dst.Visibility = visibility
	dst.AccessList = accessList
	if dst.Owner == "" && requester != "" {
		dst.Owner = requester
	}
	return s.saveCollectionJSON(&dst)
}

// UpdateCollectionPermissions modifies visibility, access policy, and PSK settings (Issue #338).
// Setting PrevSHA maintains a git-like history pointer.
func (s *AnonService) UpdateCollectionPermissions(
	hash string,
	visibility string,
	accessList []string,
	accessPolicy string,
	psk string,
	requester string,
) (string, error) {
	defer log.LogDuration("AnonService.UpdateCollectionPermissions")()
	if visibility != "" && !model.IsValidVisibility(visibility) {
		return "", fmt.Errorf("invalid visibility: %s", visibility)
	}
	if accessPolicy != "" && !model.IsValidAccessPolicy(accessPolicy) {
		return "", fmt.Errorf("invalid access policy: %s", accessPolicy)
	}
	if visibility == model.VisibilityRestricted && len(accessList) == 0 {
		return "", fmt.Errorf("access_list required for restricted visibility")
	}

	src, err := s.GetCollectionByHash(hash)
	if err != nil {
		return "", err
	}
	if src.Owner != "" && src.Owner != requester {
		log.LogWarn("anon-svc: UpdateCollectionPermissions denied hash=%s owner=%s requester=%q", hash, src.Owner, requester)
		return "", fmt.Errorf("collection not found")
	}

	dst := *src
	dst.PrevSHA = hash
	if visibility != "" {
		dst.Visibility = visibility
	}
	if accessList != nil {
		dst.AccessList = accessList
	}
	if accessPolicy != "" {
		dst.AccessPolicy = accessPolicy
	}
	if psk != "" {
		if len(psk) == 64 && isValidHash(psk) {
			dst.PSKHash = psk
		} else {
			dst.PSKHash = sha256Hex([]byte(psk))
		}
		dst.IsProtected = true
	}
	if dst.Owner == "" && requester != "" {
		dst.Owner = requester
	}
	return s.saveCollectionJSON(&dst)
}

// inheritVisibility copies the permission triple from the source collection to the derived
// collection (used by commit/version-roll paths).
// Why a separate function: there are multiple derived paths (commit, removeEmpty branch),
// and missing any one would silently downgrade a restricted collection to public.
// Centralizing here makes review easier.
func inheritVisibility(dst, src *model.AnonCollection) {
	if dst == nil || src == nil {
		return
	}
	dst.Visibility = src.Visibility
	dst.AccessList = src.AccessList
	dst.Owner = src.Owner
	dst.AccessPolicy = src.AccessPolicy
	dst.Passcode = src.Passcode
	dst.PSKHash = src.PSKHash
	dst.IsProtected = src.IsProtected
}

func newAnonCollectionWithVersion(name string, entries []model.AnonCollectionEntry, version int, tags []string) *model.AnonCollection {
	if entries == nil {
		entries = []model.AnonCollectionEntry{}
	}
	if tags == nil {
		tags = []string{}
	}
	return &model.AnonCollection{
		Version:      version,
		FriendlyName: name,
		Entries:      entries,
		Tags:         tags,
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
	}
}

func (s *AnonService) saveCollectionJSON(coll *model.AnonCollection) (string, error) {
	sort.Slice(coll.Entries, func(i, j int) bool {
		return coll.Entries[i].Path < coll.Entries[j].Path
	})

	jsonBytes, err := json.MarshalIndent(coll, "", "  ")
	if err != nil {
		return "", err
	}

	hash := sha256Hex(jsonBytes)

	targetDir := filepath.Join(s.config.StorageDir, hash[:2])
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return "", err
	}
	targetPath := filepath.Join(targetDir, hash)
	if err := os.WriteFile(targetPath, jsonBytes, 0644); err != nil {
		return "", err
	}

	relPath := filepath.Join(hash[:2], hash)
	_ = repository.InsertFileMeta(&model.FileMeta{
		Hash:     hash,
		Size:     int64(len(jsonBytes)),
		MimeType: "application/json",
		Filename: "anon_collection.json",
		Gziped:   false,
		Type:     repository.FileTypeAnonCollection,
	})
	_ = repository.InsertFileProvider(hash, "local", relPath)

	return hash, nil
}
