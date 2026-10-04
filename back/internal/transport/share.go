package transport

// share.go: Node sharing-scope frame (doc/NETDISK.md M2 / ROADMAP Phase 5 "File Scope Management").
//
// Semantics: the share frame answers "what does this node expose externally" — packaged collections (with entry lists)
// and individual files. This is the "see file links" step in the
// "marketplace → join node → see file links → select and save" pipeline.
//
// Difference from the existing list verb (do NOT merge):
//   - list = local file-management index (full file_index, including absolute local paths); its semantics are
//     "which files does this node manage" and should only be open to trusted peers / local sessions;
//   - share = **explicitly declared** sharing scope, off by default, the sole entry point for publishing content externally.
// Using list as a "sharing manifest" is equivalent to publicly exposing the entire disk by default (this is also the root cause
// of the ROADMAP TODO: "broadcasting local collection hashes equals revealing what this node holds").
//
// Frame sequence:
//
//	Request: {"type":"share","reqId":"<optional>"}
//	Response: {"type":"share-resp","collections":[...],"files":[...],"dirs":[...],
//	       "total":N,"reqId":"..."}
//
// total = len(collections)+len(files); the server computes and sends it (the frontend card displays
// "N items total" directly, without having to sum two categories itself). When sharing is off, respond with an **empty**
// share-resp rather than an error: the empty state is a valid business state (the peer shared nothing), and the frontend renders
// "this node has no shared content" without needing an error branch.

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
	Hash    string            `json:"hash"`
	Name    string            `json:"name,omitempty"`
	Size    int64             `json:"size,omitempty"` // Entry count (reuses the size name to stay consistent with the frontend card)
	Tags    []string          `json:"tags,omitempty"`
	Entries []ShareEntryInfo  `json:"entries"`
}

// ShareSnapshot is the complete result of one share query.
type ShareSnapshot struct {
	Collections []ShareCollectionInfo `json:"collections"`
	Files       []ShareFileInfo       `json:"files"`
	Dirs        []string              `json:"dirs,omitempty"`
}

// shareResp is the share frame response. It embeds ShareSnapshot so the JSON is flat
// ({type,collections,files,dirs,total,reqId}), requiring only one layer of parsing on the frontend.
type shareResp struct {
	Type string `json:"type"`
	ShareSnapshot
	Total int    `json:"total"`
	ReqID string `json:"reqId,omitempty"`
}

// SetShareProvider injects the node's sharing-scope reader (main wires
// service.NodeShare.SnapshotFor). The argument is the requesting peer's ID — friends can see private entries.
// nil = sharing not enabled → the share frame returns an empty snapshot. Can be called after Start() (see field comments).
func (s *PeerJSService) SetShareProvider(p func(peerID string) ShareSnapshot) {
	s.shareMu.Lock()
	s.shareProvider = p
	s.shareMu.Unlock()
}

// currentShareProvider takes a locked snapshot of the sharing-scope reader.
func (s *PeerJSService) currentShareProvider() func(peerID string) ShareSnapshot {
	s.shareMu.RLock()
	defer s.shareMu.RUnlock()
	return s.shareProvider
}

// ShareGate is a download gate: determines whether a given hash can be sent to the requester (doc/NETDISK.md §12.6).
//
// Why separate the manifest from downloads: the essential difference between the three tiers lies in "list or not" and "give or not"
// (unlisted = don't list but give). A single provider can only answer "what's in the manifest," not
// "can this hash be fetched."
//
// Only private blocks access: public / unlisted / undeclared all pass through — content-addressed retrieval is this
// system's existing behavior, and PSK is the admission gate. Blocking "undeclared" would even break basic
// self-checks like "upload → retrieve by hash to verify."
//
// self = this node's local channel (HTTP management API / local WS direct connection), always "self."
type ShareGate interface {
	AllowsDownload(peerID, hash string, self bool) bool
}

// SetShareGate injects the download gate (main wires service.NodeShare). nil = no gating.
func (s *PeerJSService) SetShareGate(g ShareGate) {
	s.shareMu.Lock()
	s.shareGate = g
	s.shareMu.Unlock()
}

// currentShareGate takes a locked snapshot of the download gate.
func (s *PeerJSService) currentShareGate() ShareGate {
	s.shareMu.RLock()
	defer s.shareMu.RUnlock()
	return s.shareGate
}

// isSelfSession determines whether the session comes from "self" (local WS direct connection, i.e., the admin panel/dashboard
// connecting via /ws/peer locally).
//
// Why it's needed: the semantics of private is "only self and friends can download." On a P2P connection, the only
// identifier available is the peer's self-reported peer id. The operator's own panel also gets a random id (which may differ each time),
// so identity cannot be determined by id. A connection coming through the local WS is this node's management channel by definition,
// and it IS "self."
func isSelfSession(c Session) bool {
	ls, ok := c.(interface{ IsLocal() bool })
	return ok && ls.IsLocal()
}

// shareLoadInfo is the sharing summary reported during announce (loadInfo.shares); it contains only **counts**.
// Why not report specific hashes: announce is broadcast by the discovery server to all queryers; reporting hashes
// would publicly reveal "what this node holds." Counts are sufficient for the marketplace card's guiding info; details are obtained
// via the share frame (point-to-point) after the user joins and connects directly.
//
// Note: this function is registered as a callback to HTTPDiscovery (announce heartbeat called every 30s);
// each call re-reads the provider — wiring after Start (main's order) also takes effect.
//
// Here it counts from an **anonymous viewpoint** (empty peerID): announce counts are broadcast by the discovery server to
// all queryers; we must not expose private entry counts just because "some queryer happens to be a friend" —
// that would publicly reveal "I have N things shared only with friends."
func (s *PeerJSService) shareLoadInfo() map[string]any {
	p := s.currentShareProvider()
	if p == nil {
		return nil
	}
	// 2026-10-04: when no PSK is configured this node serves **anyone** who reaches it
	// (transport/psk.go pskEnabled() is literally cfg.PeerPSK != ""), so the announce body is a
	// public description of a node that has no admission control at all. Measured on the live
	// deployment: GET /status returned this node's dir/file/collection counts to any caller.
	//
	// Degrade rather than disable: the counts are what the market card shows to help someone
	// decide whether to connect, and the node still needs to be **findable**. Dropping just the
	// counts keeps discovery working while removing the "here is exactly what this node holds"
	// signal. Operators who want the counts published opt in by setting a PSK (the counts then
	// sit behind a gate that exists) or, once tokenized signaling lands, a signaling token.
	//
	// Boundary: this is metadata reduction, not access control. It deliberately does NOT try to
	// replace a gate — with no PSK the content is still reachable, and the honest statement of that
	// is the startup warning from cmd/server/security_status.go.
	if s.cfg == nil || s.cfg.PeerPSK == "" {
		return map[string]any{
			"shares": map[string]any{
				"countsHidden": true,
				"reason":       "no PSK configured — counts hidden from the public roster",
			},
		}
	}
	snap := p("")
	return map[string]any{
		"shares": map[string]any{
			"collections": len(snap.Collections),
			"files":       len(snap.Files),
			"dirs":        len(snap.Dirs),
		},
	}
}

// serveShare answers the peer's share query (inbound role).
//
// The requester's identity is only a **self-reported** peer id (ROADMAP hard constraint: no account
// dependency before Phase 7, and no signature to verify). It can do only one thing: let people on the friends list see private
// entries — anything more would be impersonating an identity. unlisted is never listed, public is always listed;
// this filtering is done inside service.NodeShare.SnapshotFor.
func (s *PeerJSService) serveShare(c Session, r dcResp) {
	snap := ShareSnapshot{}
	if p := s.currentShareProvider(); p != nil {
		snap = p(c.ID())
	}
	// Ensure JSON contains [] instead of null: the frontend list renderer doesn't need null checks
	if snap.Collections == nil {
		snap.Collections = []ShareCollectionInfo{}
	}
	if snap.Files == nil {
		snap.Files = []ShareFileInfo{}
	}
	total := len(snap.Collections) + len(snap.Files)
	_ = c.SendJSON(shareResp{Type: "share-resp", ShareSnapshot: snap, Total: total, ReqID: r.ReqID})
}
