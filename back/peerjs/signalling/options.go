package signalling

import "time"

// Options is the signaling client configuration.
//
// Only transport-level fields appear here. Options.ID is intentionally absent —
// the node ID is the id argument of NewPeerJSSignaller, which is authoritative
// (an empty id means the server assigns one via the HTTP retrieve step).
// ICE servers are likewise absent: they belong to the WebRTC layer one level up
// (peerjs.Options.ICEServers), and this package must not import pion/webrtc.
type Options struct {
	Host         string // signaling server address (default 0.peerjs.com)
	Port         string
	Secure       bool          // wss/https
	Path         string        // path prefix for self-hosted server (default "/")
	Key          string        // API key (default "peerjs")
	Token        string        // empty means randomly generated
	PingInterval time.Duration // signaling heartbeat interval (default 5s)
}

// DefaultOptions returns the PeerJS public cloud default configuration.
// Nodes and the panel must default to the same signaling, otherwise neither can find the other.
// Self-hosted deployments override via config (PEERDRIVE_PEERJS_HOST/PORT/KEY).
//
// These are the single source of truth for the defaults; peerjs.DefaultOptions
// delegates here so the values cannot drift.
func DefaultOptions() Options {
	return Options{
		Host:         "0.peerjs.com",
		Port:         "9000",
		Secure:       true,
		Path:         "/",
		Key:          "peerjs",
		PingInterval: 5 * time.Second,
	}
}

// NormalizeOptions fills in default values. A zero Token is replaced with a
// random one so two clients sharing a config cannot register under the same token.
func NormalizeOptions(opts Options) Options {
	if opts.Port == "" {
		opts.Port = "443"
	}
	if opts.Path == "" {
		opts.Path = "/"
	}
	if opts.Key == "" {
		opts.Key = "peerjs"
	}
	if opts.PingInterval == 0 {
		opts.PingInterval = 5 * time.Second
	}
	if opts.Token == "" {
		opts.Token = randomToken()
	}
	return opts
}
