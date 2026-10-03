package service

// nodeshare.go: Node share scope resolution (doc/NETDISK.md M2 / ROADMAP Phase 5).
//
// Responsibility: resolve the operator's share declarations into a ShareSnapshot, consumed by:
//   - share frame (peer asks "what did you share") -> transport.PeerJSService.SetShareProvider
//   - announce loadInfo summary (counts only) -> ShareSummary
//
// Disabled by default (PEERDRIVE_SHARE_ENABLE=false): no share manifest is exposed unless explicitly enabled.
//
// Share scope is composed from **two sources**, the latter overriding the former (doc/NETDISK.md M2.6):
//  1. Environment variables (PEERDRIVE_SHARE_*) -- just **initial values**: seeded into runtime
//     state and persisted on first startup; changing env vars afterwards will not revert the
//     already-selected scope;
//  2. Runtime selection (admin console checkboxes / PUT /peerjs/share) -- persisted to
//     share_scope.json under storage, survives restarts.
//
// Why runtime changes must be possible: share scope is "which files I'm willing to give out" --
// inherently an on-the-fly decision (want to share a newly uploaded file immediately, or stop
// sharing a directory). Requiring env var changes + node restart turns this into an ops task --
// the real result is nobody changes it, leading to either long-term sharing of an overly broad
// directory, or not enabling sharing at all.
//
// Granularity (three independent sources):
//   - dirs: entire directories (all files under that path in file_index are shared)
//   - files: files selected individually by hash (need not be inside a shared directory)
//   - collections: collections (64hex or "all" = all public collections)
//
// Levels (each declaration has one, see model.Level* and doc/NETDISK.md section 12.6):
//   - public   -- appears in the share manifest, anyone can download
//   - unlisted -- does not appear in the manifest, knowing the hash allows download
//   - private  -- does not appear in the manifest, only self and friends can download
//
// Friends = whitelist of node IDs in ShareScope.Friends. "Self" = local channel not via P2P
// (HTTP admin API, local machine WS direct connection), determined by the transport layer and
// passed in (see NodeShare.AllowsDownload's self parameter).
//
// Security boundaries (two, do not relax):
//  1. Share frames carry no verifiable identity (ROADMAP hard constraint: no account dependency
//     before Phase 7). Peer id is self-reported by the peer. Therefore private determination is
//     only meaningful on **PSK-admitted** connections -- without PSK, anyone can connect, and the
//     friends list degrades to "whoever claims to be this id". Strong identity requires the account
//     system; don't pretend it exists here.
//  2. When a collection's own visibility is not public (restricted/private), it does not enter
//     the external manifest -- AccessList is an account list, and without identity it cannot be
//     verified. Such collections are treated as private: only friends can download, not listed.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"peerdrive/internal/pathutil"

	"peerdrive/internal/config"
	"peerdrive/internal/log"
	"peerdrive/internal/model"
	"peerdrive/internal/transport"
)

// shareAllToken represents "all public collections" in collection configuration.
const shareAllToken = "all"

// shareScopeFile is the filename for runtime share scope (located under storageDir).
// Same format idea as node_directory's joined_nodes.json: small file, atomic semantics
// guaranteed by the caller (temp file + rename).
const shareScopeFile = "share_scope.json"

// levelCacheTTL is the level cache TTL.
//
// Why expiry is needed: directory sharing levels are computed from "the file's current path",
// but files may be uploaded after the scope is set (share directory first, then put files in).
// Without expiry, newly added files would be treated as "undeclared" -- and "undeclared"
// defaults to downloadable, so new files in a private directory would be accessible to anyone.
// 10s is a trade-off between "at most 10s of leakage" and "scanning the index on every
// download"; scope changes invalidate immediately (see persistLocked), only external file
// additions wait for expiry.
const levelCacheTTL = 10 * time.Second

// ShareItem is a share declaration: target + level.
//
// JSON supports two formats (UnmarshalJSON):
//   - String (legacy disk format / env var seeding): `"/data/media"` -> level public
//   - Object: {"id":"/data/media","level":"unlisted"}
//
// Why compatibility with strings is needed: share_scope.json written before this upgrade only
// has id. Not understanding it would fall back to env var defaults, effectively discarding the
// carefully selected scope (see NewNodeShare comment).
type ShareItem struct {
	ID    string `json:"id"`
	Level string `json:"level,omitempty"`
}

// UnmarshalJSON supports both string and object formats.
func (i *ShareItem) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		i.ID = strings.TrimSpace(s)
		i.Level = ""
		return nil
	}
	var o struct {
		ID    string `json:"id"`
		Level string `json:"level"`
	}
	if err := json.Unmarshal(b, &o); err != nil {
		return err
	}
	i.ID = strings.TrimSpace(o.ID)
	i.Level = strings.TrimSpace(o.Level)
	return nil
}

// EffectiveLevel returns the normalized level (empty -> public).
func (i ShareItem) EffectiveLevel() string { return model.NormalizeLevel(i.Level) }

// ShareScope is the operator-selected share scope.
//
// Three sources are independent and take the union: dirs (entire directories) + files (individual
// files) + collections. Empty dirs does NOT mean "no filtering" (that would be "share everything"),
// but "don't share by directory" -- see filesSnapshotFor's comment.
//
// Friends is the allowlist for private level (node IDs).
type ShareScope struct {
	Enable      bool        `json:"enable"`
	Dirs        []ShareItem `json:"dirs"`
	Files       []ShareItem `json:"files"`
	Collections []ShareItem `json:"collections"`
	Friends     []string    `json:"friends"`
}

// NodeShare resolves share scope and provides share frame data and download permissions.
type NodeShare struct {
	storageDir string
	path       string // absolute path of share_scope.json
	cfg        *config.Config

	// Dependencies (injected, nil-safe)
	fileList     func() ([]transport.FileInfo, error)
	fileInfo     func(hash string) (*transport.FileInfo, error)
	anonGet      func(hash string) (*model.AnonCollection, error)
	anonList     func() ([]model.AnonCollectionSummary, error)
	resolveFiles func(sc ShareScope) []transport.FileInfo

	mu      sync.Mutex
	scope   ShareScope
	levels  map[string]string // hash -> level cache
	levelsAt time.Time
}

// NewNodeShare creates the share service.
//
// Seeding order: env vars are initial values, share_scope.json is runtime selection.
// On first startup, env vars are seeded into scope and persisted. On subsequent startups,
// share_scope.json takes precedence (it reflects the operator's latest selection);
// only when the file doesn't exist are env vars used again.
func NewNodeShare(cfg *config.Config) *NodeShare {
	s := &NodeShare{
		storageDir: cfg.StorageDir,
		path:       filepath.Join(cfg.StorageDir, shareScopeFile),
		cfg:        cfg,
	}
	if scope, ok := s.load(); ok {
		s.scope = scope
	} else {
		s.scope = s.seedFromEnv()
		// First startup: seed env vars and persist
		if err := s.save(s.scope); err != nil {
			log.LogWarn("nodeshare: initial seed persist failed: %v", err)
		}
	}
	return s
}

// seedFromEnv reads PEERDRIVE_SHARE_* env vars as initial values.
func (s *NodeShare) seedFromEnv() ShareScope {
	if s.cfg == nil {
		return ShareScope{Enable: false}
	}
	return ShareScope{
		Enable: s.cfg.ShareEnable,
		Dirs:   []ShareItem{},
		Files:  []ShareItem{},
		Collections: []ShareItem{},
		Friends: []string{},
	}
}

// SetFileList injects the file index reader.
func (s *NodeShare) SetFileList(fn func() ([]transport.FileInfo, error)) { s.fileList = fn }

// SetFileInfo injects single-file info lookup.
func (s *NodeShare) SetFileInfo(fn func(hash string) (*transport.FileInfo, error)) { s.fileInfo = fn }

// SetAnonGet injects anonymous collection lookup.
func (s *NodeShare) SetAnonGet(fn func(hash string) (*model.AnonCollection, error)) { s.anonGet = fn }

// SetAnonList injects anonymous collection listing.
func (s *NodeShare) SetAnonList(fn func() ([]model.AnonCollectionSummary, error)) { s.anonList = fn }

// SetResolveFiles injects a custom file resolver (for testing or future overrides).
func (s *NodeShare) SetResolveFiles(fn func(sc ShareScope) []transport.FileInfo) { s.resolveFiles = fn }

// Scope returns the current share scope.
func (s *NodeShare) Scope() ShareScope {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scope
}

// SetScope updates the share scope with full validation, returning an error on invalid input.
// Validation fails atomically: invalid input results in no changes to the current scope.
func (s *NodeShare) SetScope(sc ShareScope) error {
	dirs, err := normalizeDirs(idsOf(sc.Dirs))
	if err != nil {
		return err
	}
	files, err := normalizeFileHashes(idsOf(sc.Files))
	if err != nil {
		return err
	}
	colls, err := normalizeCollections(idsOf(sc.Collections))
	if err != nil {
		return err
	}
	friends, err := normalizeFriends(sc.Friends)
	if err != nil {
		return err
	}
	if err := validateLevels(sc.Dirs); err != nil {
		return err
	}
	if err := validateLevels(sc.Files); err != nil {
		return err
	}
	if err := validateLevels(sc.Collections); err != nil {
		return err
	}
	newScope := ShareScope{
		Enable:      sc.Enable,
		Dirs:        withLevels(sc.Dirs, dirs),
		Files:       withLevels(sc.Files, files),
		Collections: withLevels(sc.Collections, colls),
		Friends:     friends,
	}
	s.mu.Lock()
	s.scope = newScope
	s.levels = nil // Invalidate cache on scope change
	s.mu.Unlock()
	if err := s.save(newScope); err != nil {
		log.LogWarn("nodeshare: save scope failed: %v", err)
	}
	log.LogInfo("nodeshare: scope updated enable=%v dirs=%d files=%d collections=%d friends=%d",
		newScope.Enable, len(newScope.Dirs), len(newScope.Files), len(newScope.Collections), len(newScope.Friends))
	return nil
}

// isFriend checks whether peerID is in the friends allowlist (caller holds the scope).
func (s ShareScope) isFriend(peerID string) bool {
	if peerID == "" {
		return false
	}
	for _, f := range s.Friends {
		if f == peerID {
			return true
		}
	}
	return false
}

// ---- Persistence ----

// load reads the scope from disk. Returns (scope, ok).
//
// Returning ok=false means the file doesn't exist (first run) or is corrupted (not a
// usable state). Corrupted files are not deleted (manual inspection possible), treated as
// "never saved" -- consistent with node_directory.
func (s *NodeShare) load() (ShareScope, bool) {
	if s.path == "" {
		return ShareScope{}, false
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.LogWarn("nodeshare: read %s failed: %v", s.path, err)
		}
		return ShareScope{}, false
	}
	var sc ShareScope
	if err := json.Unmarshal(raw, &sc); err != nil {
		log.LogWarn("nodeshare: parse %s failed: %v", s.path, err)
		return ShareScope{}, false
	}
	// Disk content must also pass validation: manually edited files shouldn't bring illegal
	// values into the share manifest
	dirs, err := normalizeDirs(idsOf(sc.Dirs))
	if err != nil {
		log.LogWarn("nodeshare: %v", err)
		return ShareScope{}, false
	}
	files, err := normalizeFileHashes(idsOf(sc.Files))
	if err != nil {
		log.LogWarn("nodeshare: %v", err)
		return ShareScope{}, false
	}
	colls, err := normalizeCollections(idsOf(sc.Collections))
	if err != nil {
		log.LogWarn("nodeshare: %v", err)
		return ShareScope{}, false
	}
	friends, err := normalizeFriends(sc.Friends)
	if err != nil {
		log.LogWarn("nodeshare: %v", err)
		return ShareScope{}, false
	}
	// Invalid level -> fall back to public (disk files represent "previously effective state",
	// better to be permissive than to silently make content unavailable; different from HTTP
	// entry's "reject entire batch", where the user just filled it in)
	dirItems := withLevels(sc.Dirs, dirs)
	fileItems := withLevels(sc.Files, files)
	collItems := withLevels(sc.Collections, colls)
	return ShareScope{Enable: sc.Enable, Dirs: dirItems, Files: fileItems, Collections: collItems, Friends: friends}, true
}

// save writes to disk atomically: temp file + rename.
// Why not WriteFile directly: a process kill / power outage would leave partial JSON, and the
// next startup would fail to parse it, silently falling back to env var defaults, and the
// carefully selected share scope would be lost.
func (s *NodeShare) save(sc ShareScope) error {
	raw, err := json.MarshalIndent(sc, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	// Directory may not exist yet (node just started, no files uploaded/pulled yet): create it
	// first, otherwise first save would ENOENT. Use pathutil's safe mkdir (parent directory as root).
	if err := pathutil.SafeMkdirAllAny([]string{filepath.Dir(dir)}, dir, 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	// Use pathutil's safe write: path comes from config (storageDir), disk write and boundary
	// check use the same approach, no "check then write by path" TOCTOU window.
	if err := pathutil.SafeWriteFileAny([]string{dir}, tmp, raw, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, s.path)
}

// Snapshot resolves the current share scope (anonymous perspective, for use outside share frames).
func (s *NodeShare) Snapshot() transport.ShareSnapshot { return s.SnapshotFor("") }

// SnapshotFor resolves the share scope visible to the requester (share frame data source).
//
// Why peerID is needed: share frames are point-to-point direct connections, and the requester's
// node ID is known (carried in the connection), so "friends can see my private manifest" is
// implementable -- friends also need to know what's available, otherwise private would be
// "granted permission but no catalog".
//
// Sharing disabled -> empty snapshot (not an error: peer not sharing is a valid business state).
func (s *NodeShare) SnapshotFor(peerID string) transport.ShareSnapshot {
	snap := transport.ShareSnapshot{
		Collections: []transport.ShareCollectionInfo{},
		Files:       []transport.ShareFileInfo{},
	}
	sc := s.Scope()
	if !sc.Enable {
		return snap
	}
	friend := sc.isFriend(peerID)
	snap.Collections = s.collectionsSnapshotFor(sc.Collections, friend)
	snap.Files = s.filesSnapshotFor(sc, friend)
	// Only return path summaries of public directories: the existence of private/unlisted
	// directories is itself information
	for _, d := range sc.Dirs {
		if d.EffectiveLevel() == model.LevelPublic {
			snap.Dirs = append(snap.Dirs, d.ID)
		}
	}
	return snap
}

// Summary returns the share summary (for announce loadInfo, counts only).
func (s *NodeShare) Summary() model.NodeShares {
	snap := s.Snapshot()
	return model.NodeShares{
		Collections: len(snap.Collections),
		Files:       len(snap.Files),
		Dirs:        len(snap.Dirs),
	}
}

// LevelOf returns the share level of a hash (undeclared -> empty string).
func (s *NodeShare) LevelOf(hash string) string {
	if hash == "" {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.levelMapLocked()[hash]
}

// AllowsDownload determines whether the requester can download the hash (transport req gate).
//
// Only private blocks: public / unlisted / **undeclared** are all allowed.
// Why undeclared is also allowed: content-addressed retrieval (knowing the hash allows retrieval)
// is existing system behavior; PSK is the admission gate. Changing to "must declare to retrieve"
// would break even the most basic self-check ("upload -> retrieve by hash to verify"), and would
// also break all old node downloads after upgrade.
//
// self = local channel on this node (HTTP admin API / local WS direct connection), determined by transport.
func (s *NodeShare) AllowsDownload(peerID, hash string, self bool) bool {
	if self {
		return true
	}
	if s.LevelOf(hash) != model.LevelPrivate {
		return true
	}
	return s.Scope().isFriend(peerID)
}

// levelMapLocked builds/returns the level cache (caller holds the lock).
func (s *NodeShare) levelMapLocked() map[string]string {
	if s.levels != nil && time.Since(s.levelsAt) <= levelCacheTTL {
		return s.levels
	}
	m := make(map[string]string, len(s.scope.Files)+8)
	// Single file selection: record by hash directly
	for _, it := range s.scope.Files {
		if it.ID == "" {
			continue
		}
		m[it.ID] = model.LoosestLevel(m[it.ID], it.EffectiveLevel())
	}
	// Directories: files under a directory inherit that directory's level (multiple -> loosest)
	if len(s.scope.Dirs) > 0 {
		for _, f := range s.resolveFiles(s.scope) {
			if f.Path == "" || f.Delete {
				continue
			}
			if lvl := dirLevelFor(s.scope.Dirs, f.Path); lvl != "" {
				m[f.Hash] = model.LoosestLevel(m[f.Hash], lvl)
			}
		}
	}
	// Collections: entry hashes inherit collection level (collection non-public -> demoted to private)
	for _, it := range s.scope.Collections {
		lvl := collectionLevelLocked(s, it)
		if lvl == "" {
			continue
		}
		for _, h := range s.collectionHashesLocked(it.ID) {
			m[h] = model.LoosestLevel(m[h], lvl)
		}
	}
	s.levels = m
	s.levelsAt = time.Now()
	return m
}

// collectionLevelLocked returns the effective level of a collection entry (including "collection
// own visibility demotion").
func collectionLevelLocked(s *NodeShare, it ShareItem) string {
	if it.ID == "" || s.anonGet == nil {
		return ""
	}
	lvl := it.EffectiveLevel()
	if it.ID == shareAllToken {
		return lvl
	}
	coll, err := s.anonGet(it.ID)
	if err != nil || coll == nil {
		return ""
	}
	// AccessList cannot be verified (no identity) -> restricted/private collections treated as
	// private: not listed, only friends can download. See file header security boundary item 2.
	if vis := coll.EffectiveVisibility(); vis != model.VisibilityPublic {
		return model.LevelPrivate
	}
	return lvl
}

// collectionHashesLocked expands a collection's entry hashes ("all" expands to all public collections).
func (s *NodeShare) collectionHashesLocked(id string) []string {
	if s.anonGet == nil {
		return nil
	}
	ids := []string{id}
	if id == shareAllToken {
		if s.anonList == nil {
			return nil
		}
		all, err := s.anonList()
		if err != nil {
			log.LogWarn("nodeshare: list collections failed: %v", err)
			return nil
		}
		ids = make([]string, 0, len(all))
		for _, c := range all {
			// Empty visibility treated as public (consistent with model.EffectiveVisibility)
			if c.Visibility == "" || c.Visibility == model.VisibilityPublic {
				ids = append(ids, c.Hash)
			}
		}
	}
	out := make([]string, 0, len(ids))
	for _, h := range ids {
		coll, err := s.anonGet(h)
		if err != nil || coll == nil {
			continue
		}
		// The collection's own hash is also included: the manifest is a content-addressed JSON,
		// retrievable by hash alone (panel collection links rely on this). Missing it would let
		// private collection manifests be fetched by strangers: content is still blocked by entry
		// levels, but entry paths and hashes are all leaked.
		out = append(out, h)
		for _, e := range coll.Entries {
			if eh := e.GetPrimaryHash(); eh != "" {
				out = append(out, eh)
			}
		}
	}
	return out
}

// CandidateFiles returns the selectable file list (admin console checkbox data source): files from
// file_index + whether each file is currently shared and at what level.
//
// Why compute shared here instead of letting the frontend compare two lists: directory sharing and
// single-file sharing are two sources, and the frontend would need to reimplement directory prefix
// matching to correctly show checkbox states -- that implementation would inevitably diverge from
// the backend (Windows drive letter case, `/` vs `\` mixing are all pitfalls).
func (s *NodeShare) CandidateFiles() []ShareFileItem {
	sc := s.Scope()
	files := s.resolveFiles(sc)
	sel := make(map[string]ShareItem, len(sc.Files))
	for _, it := range sc.Files {
		sel[it.ID] = it
	}
	out := make([]ShareFileItem, 0, len(files))
	dirs := idsOf(sc.Dirs)
	for _, f := range files {
		if f.Path == "" || f.Delete {
			continue
		}
		byDir := pathutil.WithinAny(dirs, f.Path)
		_, picked := sel[f.Hash]
		// 2026-09-26: When share directories are declared, files outside directories and not
		// manually selected are no longer candidates -- switching share directories won't pull in
		// old directory files; manually selected files are always retained.
		if len(dirs) > 0 && !byDir && !picked {
			continue
		}
		shared := false
		lvl := ""
		if it, ok := sel[f.Hash]; ok {
			shared = true
			lvl = model.LoosestLevel(lvl, it.EffectiveLevel())
		}
		if dl := dirLevelFor(sc.Dirs, f.Path); dl != "" {
			shared = true
			lvl = model.LoosestLevel(lvl, dl)
		}
		out = append(out, ShareFileItem{
			Hash:   f.Hash,
			Name:   f.Name,
			Size:   f.Size,
			Shared: shared,
			ByDir:  byDir,
			Level:  lvl,
		})
	}
	return out
}

// ShareFileItem is a selectable file with its share status (admin console data source).
type ShareFileItem struct {
	Hash   string `json:"hash"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Shared bool   `json:"shared"`
	ByDir  bool   `json:"by_dir"` // Whether it's shared via a directory (vs individual selection)
	Level  string `json:"level,omitempty"`
}

// collectionsSnapshotFor resolves the collection share manifest (given a declaration list).
//
// friend: when the requester is a friend, private-level collections are also listed (otherwise
// friends get permission but don't know what's available). unlisted is never listed -- its
// semantic is "not listed".
func (s *NodeShare) collectionsSnapshotFor(items []ShareItem, friend bool) []transport.ShareCollectionInfo {
	if s.anonGet == nil {
		return []transport.ShareCollectionInfo{}
	}
	list := make([]ShareItem, 0, len(items))
	for _, it := range items {
		if it.ID == shareAllToken {
			// "all" = all public collections, level inherits from this declaration
			if s.anonList == nil {
				return []transport.ShareCollectionInfo{}
			}
			all, err := s.anonList()
			if err != nil {
				log.LogWarn("nodeshare: list collections failed: %v", err)
				return []transport.ShareCollectionInfo{}
			}
			for _, c := range all {
				// Empty visibility treated as public (consistent with model.EffectiveVisibility)
				if c.Visibility == "" || c.Visibility == model.VisibilityPublic {
					list = append(list, ShareItem{ID: c.Hash, Level: it.Level})
				}
			}
			continue
		}
		list = append(list, it)
	}

	out := make([]transport.ShareCollectionInfo, 0, len(list))
	for _, it := range list {
		coll, err := s.anonGet(it.ID)
		if err != nil || coll == nil {
			// Explicitly declared but unreadable (deleted/wrong hash): skip and warn, don't fake entries
			log.LogDebug("nodeshare: collection %s not readable: %v", it.ID, err)
			continue
		}
		lvl := it.EffectiveLevel()
		if vis := coll.EffectiveVisibility(); vis != model.VisibilityPublic {
			// See file header security boundary item 2: AccessList cannot be verified -> treat as private
			lvl = model.LevelPrivate
		}
		if lvl == model.LevelUnlisted || (lvl == model.LevelPrivate && !friend) {
			continue
		}
		info := transport.ShareCollectionInfo{
			Hash:    it.ID,
			Name:    coll.FriendlyName,
			Tags:    coll.Tags,
			Entries: make([]transport.ShareEntryInfo, 0, len(coll.Entries)),
		}
		for _, e := range coll.Entries {
			info.Entries = append(info.Entries, transport.ShareEntryInfo{
				Path: e.Path,
				Hash: e.GetPrimaryHash(),
				Mime: e.GetPrimaryMime(),
			})
		}
		info.Size = int64(len(info.Entries))
		out = append(out, info)
	}
	return out
}

// filesSnapshotFor filters registered files by "directory prefix UNION single-file selection",
// then decides whether they enter the manifest based on level.
//
// Why prefix matching is sufficient: files themselves come from file_index (already passed
// IsPathAllowed at registration -- must be within the upload root directory). Here we're just
// "drawing a smaller externally visible subset within the upload root directory". Actual read
// authorization is backed by serveFile's path validation.
//
// Empty dirs AND empty files = share no files (not share everything): this is "default off" at
// the file dimension. Treating empty as "no filtering" would mean PEERDRIVE_SHARE_ENABLE=true
// leaks the entire file_index.
func (s *NodeShare) filesSnapshotFor(sc ShareScope, friend bool) []transport.ShareFileInfo {
	files := s.resolveFiles(sc)
	sel := make(map[string]ShareItem, len(sc.Files))
	for _, it := range sc.Files {
		sel[it.ID] = it
	}
	out := make([]transport.ShareFileInfo, 0, len(files))
	for _, f := range files {
		if f.Path == "" || f.Delete {
			continue
		}
		// Single file selection by hash: independent of directories, can share "files not in any
		// shared directory". Directory matching uses pathutil.WithinAny instead of hand-written
		// prefix comparison (reasons in underShareDir).
		lvl := ""
		if it, ok := sel[f.Hash]; ok {
			lvl = model.LoosestLevel(lvl, it.EffectiveLevel())
		}
		if dl := dirLevelFor(sc.Dirs, f.Path); dl != "" {
			lvl = model.LoosestLevel(lvl, dl)
		}
		if lvl == "" || lvl == model.LevelUnlisted || (lvl == model.LevelPrivate && !friend) {
			continue
		}
		out = append(out, transport.ShareFileInfo{
			Hash: f.Hash,
			Name: f.Name,
			// Only return relative display path, not local absolute path (external minimum
			// information principle, same consideration as inbound.go's redactDisallowedPath)
			Path: filepath.Base(f.Path),
			Size: f.Size,
		})
	}
	return out
}

// resolveFiles aggregates "files that might be shared": index page (with limit) UNION files
// selected by hash.
//
// Why union the latter: fileList has a 1000-entry limit (preventing DoS via remote list verb).
// When a node has more than 1000 registered files, newly uploaded files won't be on that page.
// Filtering by only that page means selected files can't be listed or shared -- the observed
// symptom is "I selected it, but nothing happened". Per-hash lookup isn't affected by pagination,
// so selected files always count.
func (s *NodeShare) resolveFiles(sc ShareScope) []transport.FileInfo {
	out := make([]transport.FileInfo, 0, len(sc.Files))
	seen := make(map[string]bool, len(sc.Files))
	if s.fileList != nil {
		files, err := s.fileList()
		if err != nil {
			log.LogWarn("nodeshare: list files failed: %v", err)
		}
		for _, f := range files {
			if f.Hash == "" || seen[f.Hash] {
				continue
			}
			seen[f.Hash] = true
			out = append(out, f)
		}
	}
	if s.fileInfo == nil {
		return out
	}
	for _, it := range sc.Files {
		if seen[it.ID] {
			continue
		}
		fi, err := s.fileInfo(it.ID)
		if err != nil || fi == nil {
			log.LogDebug("nodeshare: selected file %s not readable: %v", it.ID, err)
			continue
		}
		if fi.Path == "" || fi.Delete {
			continue
		}
		seen[fi.Hash] = true
		out = append(out, *fi)
	}
	return out
}

// underShareDir checks whether a registered file's path falls within a share directory (kept for
// external/tests).
//
// Uses pathutil.Within instead of hand-written prefix comparison: the hand-written version only
// recognizes `dir + separator`, which misses on Windows (drive letter case, `/` vs `\` mixing),
// and can't catch same-prefix directories like `/a/shared-secret`. Share manifest and read side
// must use the same check, otherwise "listed but pull fails" inconsistency recurs.
func (s *NodeShare) underShareDir(path string) bool {
	return pathutil.WithinAny(idsOf(s.Scope().Dirs), path)
}

// ---- Validation/Normalization ----

// idsOf extracts ids from a declaration list (for pathutil / legacy interfaces).
func idsOf(items []ShareItem) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		if it.ID != "" {
			out = append(out, it.ID)
		}
	}
	return out
}

// withLevels maps levels from the request back to the normalized id list.
//
// Why not include levels during normalization: normalization (dedup/sort/validate) is done by id.
// When an id appears twice in a request (once public, once private), the loosest is taken,
// consistent with "same file hit by both directory and single file".
func withLevels(src []ShareItem, ids []string) []ShareItem {
	lv := make(map[string]string, len(src))
	for _, it := range src {
		id := strings.TrimSpace(it.ID)
		if id == "" {
			continue
		}
		lv[id] = model.LoosestLevel(lv[id], it.Level)
	}
	out := make([]ShareItem, 0, len(ids))
	for _, id := range ids {
		out = append(out, ShareItem{ID: id, Level: lv[id]})
	}
	return out
}

// validateLevels validates levels in declarations (empty = use default public, invalid = error).
//
// Why invalid values can't fall back to public: levels come from HTTP request bodies. A single
// misspelled letter would take effect at the loosest level, effectively publishing content meant
// to be restricted. Better to reject the entire batch so the operator can re-enter.
func validateLevels(items []ShareItem) error {
	for _, it := range items {
		v := strings.TrimSpace(it.Level)
		if v == "" {
			continue
		}
		if model.NormalizeLevel(v) == "" {
			return fmt.Errorf("invalid share level %q (must be public / unlisted / private)", it.Level)
		}
	}
	return nil
}

// dirLevelFor returns the **loosest** level among directories the path hits (no hit -> empty string).
func dirLevelFor(dirs []ShareItem, path string) string {
	if path == "" {
		return ""
	}
	lvl := ""
	for _, d := range dirs {
		if d.ID == "" {
			continue
		}
		if pathutil.Within(d.ID, path) {
			// NormalizeLevel: legacy declarations without level (empty string) treated as public,
			// can't let "no level specified" make it disappear from the manifest
			lvl = model.LoosestLevel(lvl, model.NormalizeLevel(d.Level))
		}
	}
	return lvl
}

// normalizeDirs normalizes directory lists: remove empty, convert to absolute paths, deduplicate,
// reject volume roots.
//
// Rejecting volume roots is consistent with main.checkUnsafeRoots: configuring root as `/`
// (Windows `C:\`) means pathutil.Within would of course allow `/etc/passwd` -- that's not a wrong
// check, that's the literal intent of the config. But this value comes from HTTP request bodies,
// so a single mistake must not share an entire drive.
func normalizeDirs(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, d := range in {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		if pathutil.IsUnsafeRoot(d) {
			return nil, fmt.Errorf("refusing to set filesystem volume root %q as share directory (that means sharing the entire drive)", d)
		}
		abs, err := filepath.Abs(d)
		if err != nil {
			return nil, fmt.Errorf("invalid share directory %q: %w", d, err)
		}
		abs = filepath.Clean(abs)
		k := dirKey(abs)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, abs)
	}
	sort.Strings(out)
	return out, nil
}

// dirKey is the dedup key for directories: on Windows, drive letters and case are case-insensitive
// (`D:\Media` and `d:\media` are the same directory). On Linux, lowercasing is a harmless
// conservative practice.
func dirKey(dir string) string {
	return strings.ToLower(filepath.Clean(dir))
}

// normalizeFileHashes normalizes single-file share lists: only accepts 64hex, deduplicates, sorts.
func normalizeFileHashes(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, raw := range in {
		h := strings.ToLower(strings.TrimSpace(raw))
		if h == "" {
			continue
		}
		if !isSHA256Hex(h) {
			return nil, fmt.Errorf("invalid file hash %q (must be 64-char hex sha256)", raw)
		}
		if seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	sort.Strings(out)
	return out, nil
}

// normalizeCollections normalizes collection lists: 64hex or "all".
func normalizeCollections(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, raw := range in {
		h := strings.ToLower(strings.TrimSpace(raw))
		if h == "" {
			continue
		}
		if h == shareAllToken {
			if !seen[h] {
				seen[h] = true
				out = append(out, h)
			}
			continue
		}
		if !isSHA256Hex(h) {
			return nil, fmt.Errorf("invalid collection hash %q (must be 64-char hex, or all)", raw)
		}
		if seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	sort.Strings(out)
	return out, nil
}

// normalizeFriends normalizes friend node ID lists: remove empty, deduplicate, sort (preserving
// original case).
func normalizeFriends(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, raw := range in {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		// Whitespace in node IDs is a transcription error (can't visually distinguish `abc ` and
		// `abc`), keeping it makes "added friend but can't retrieve" un-debuggable.
		if strings.ContainsAny(id, " \t\r\n") {
			return nil, fmt.Errorf("friend node ID cannot contain whitespace: %q", raw)
		}
		k := strings.ToLower(id)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

// isSHA256Hex validates 64-char hex (lowercase/uppercase both accepted, caller already lowercased).
func isSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
