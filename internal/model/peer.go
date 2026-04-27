package model

import "time"

type PeerInfo struct {
	PeerID        string    `json:"peer_id"`
	Addrs         []string  `json:"addrs"`
	FirstSeen     time.Time `json:"first_seen"`
	LastSeen      time.Time `json:"last_seen"`
	BytesSent     int64     `json:"bytes_sent"`
	BytesRecv     int64     `json:"bytes_recv"`
	ServerVersion string    `json:"server_version"`
	Transports    []string  `json:"transports"`     // tcp, quic, ws, etc.
	RegVerified   bool      `json:"reg_verified"`    // verified by registration server
	RegUsername   string    `json:"reg_username,omitempty"`
	ConnectionDur string    `json:"connection_dur"`  // "5m30s" format
	Latency       string    `json:"latency"`         // last RTT
	UserAgent     string    `json:"user_agent,omitempty"`

	// Direction indicates who initiated the connection: "inbound" or "outbound".
	Direction string `json:"direction,omitempty"`

	// ConnectedAt is when the connection was established.
	ConnectedAt time.Time `json:"connected_at,omitempty"`

	// DisconnectReason records why the connection ended, if known.
	DisconnectReason string `json:"disconnect_reason,omitempty"`
}
