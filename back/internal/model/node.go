// node.go: Node marketplace (directory) related models.
//
// Background (2026-09-20 netdisk goal, see doc/NETDISK.md M1):
// Users want to have other people's nodes that they can join in the marketplace. The discovery server's
// /discover/nodes can already list online nodes, but that's a raw list for programs; here we define
// UI-facing "marketplace entries" — merging online status, direct connection status, whether already joined,
// and share summaries into a single row that the frontend can directly render as cards.
package model

import "time"

// NodeShares node share summary (only reports counts, not content).
//
// Why only report counts: announce's loadInfo is broadcast to all queryers via the discovery server,
// once specific hashes / filenames are reported it's essentially publicly revealing "what this node has".
// Counts are enough to support marketplace card guidance info like "this node shares 3 collections / 12 files";
// details must wait until the user actually joins and establishes a direct connection, then obtained via
// share frames (peer-to-peer).
type NodeShares struct {
	Collections int `json:"collections"`
	Files       int `json:"files"`
	Dirs        int `json:"dirs,omitempty"`
}

// NodeSummary a row in the marketplace / my nodes list.
type NodeSummary struct {
	PeerID    string     `json:"peer_id"`
	NodeType  string     `json:"node_type,omitempty"`
	LastSeen  int64      `json:"last_seen,omitempty"` // Unix seconds (timestamp from discovery server)
	Uptime    int64      `json:"uptime,omitempty"`
	Online    bool       `json:"online"`    // Discovery server considers it online
	Connected bool       `json:"connected"` // This node currently has a WebRTC direct connection
	Joined    bool       `json:"joined"`    // Operator has joined (persisted)
	JoinedAt  *time.Time `json:"joined_at,omitempty"`
	Self      bool       `json:"self,omitempty"` // This is the node itself
	Shares    NodeShares `json:"shares"`
}
