package signalserver

// DefaultKey is the signaling API key the standalone `peersignal` binary uses when
// neither -key nor (for the main binary) PEERSIGNAL_KEY is given.
//
// Why it is NOT "peerjs" (the peerjs-server / public-cloud convention):
//
// This value is the peerdrive ecosystem's *authoritative* signal key. It is the same
// string the peerdrive client hardcodes for the project signaling
// (back/peerjs/peer.go DefaultOptions) and the same default config.Load() applies to
// PEERDRIVE_PEERJS_KEY (back/internal/config/config.go DefaultSignalKey). It is also
// what the online deployment documents (AGENTS.md 「线上部署」).
//
// Before 2026-10-07 the flag default here was "peerjs" while `peerdrive all` used the
// authoritative value: the same built binary would start signaling under one key on
// `signal` and another key on `all`, so the two subcommands could not see each other
// (and neither matched a default-configured node). Unifying all server-side defaults
// to this one value removes that fork; see back/test/deployconsistency for the gate.
//
// Note this constant governs the *flag* default only. NewServer("") keeps its generic
// "peerjs" fallback on purpose: that is the library's zero-value behavior, consumed
// outside this repo (wintools), and the protocol-compatible default for a vanilla
// peerjs client. Callers that want the peerdrive convention get it from here.
const DefaultKey = "pd-signal-1edf5e05e4a52b7351392574"
