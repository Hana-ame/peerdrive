package transport

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	hashutil "peerdrive/pkg/hashutil"
)

// TestPresenceRoom_IsStrictSHA256 the presence room name must be a valid 64hex, and actually a sha256 value.
// Discovery background (interconnect layer): the presence room lets two nodes that share
// zero collections still discover each other, but it is stuffed into the announce
// collections field. peerdrive's own signalserver only trims that field without validating,
// and the production signaling is maintained by wintools with an unknown implementation --
// the day they add a "must be 64hex" check, a human-readable room name (like "_presence")
// would make **the whole announce be rejected with 400**, taking the content shard rooms
// down with it and breaking discovery entirely. This test pins "must be a valid strict
// sha256" so a later author does not turn it into a readable name.
func TestPresenceRoom_IsStrictSHA256(t *testing.T) {
	assert.True(t, hashutil.IsStrictSHA256(PresenceRoom),
		"presence room name must be 64-char lowercase hex (current %q)", PresenceRoom)
	assert.True(t, hashutil.IsValidSHA256(PresenceRoom))
}

// TestDiscoveryRooms_PresenceToggle room list = configured content shards + optional presence room.
// Discovery background (interconnect layer): node-level interconnect (the presence room) and
// content shard discovery are additive -- turning the presence room off must land exactly on
// "only the content rooms declared in config", with no leftovers, or PEERDRIVE_DISCOVER_PRESENCE=false
// would not switch cleanly.
func TestDiscoveryRooms_PresenceToggle(t *testing.T) {
	const coll = "1111111111111111111111111111111111111111111111111111111111111111"

	t.Run("when presence room is enabled, added and deduplicated", func(t *testing.T) {
		svc := newTestPeerJSService(t)
		svc.cfg.MQTTCollections = coll
		svc.cfg.DiscoverPresence = true

		rooms := svc.discoveryRooms()
		assert.Equal(t, []string{coll, PresenceRoom}, rooms)
	})

	t.Run("when presence room is off, only config rooms", func(t *testing.T) {
		svc := newTestPeerJSService(t)
		svc.cfg.MQTTCollections = coll
		svc.cfg.DiscoverPresence = false

		rooms := svc.discoveryRooms()
		assert.Equal(t, []string{coll}, rooms)
	})

	t.Run("when no content room is configured, only presence room remains", func(t *testing.T) {
		svc := newTestPeerJSService(t)
		svc.cfg.MQTTCollections = ""
		svc.cfg.DiscoverPresence = true

		// This is exactly the default deployment shape: the interconnect layer must work standalone when there is "no shared content hash".
		rooms := svc.discoveryRooms()
		require.Equal(t, []string{PresenceRoom}, rooms)
	})

	t.Run("when operator mistakenly writes presence room into config, no duplication", func(t *testing.T) {
		svc := newTestPeerJSService(t)
		svc.cfg.MQTTCollections = PresenceRoom
		svc.cfg.DiscoverPresence = true

		assert.Equal(t, []string{PresenceRoom}, svc.discoveryRooms())
	})

	t.Run("illegal content room is filtered but presence room is still there", func(t *testing.T) {
		svc := newTestPeerJSService(t)
		svc.cfg.MQTTCollections = "not-a-hash," + coll
		svc.cfg.DiscoverPresence = true

		assert.Equal(t, []string{coll, PresenceRoom}, svc.discoveryRooms())
	})
}

// TestDiscoveryDialAllowed_MaxPeers the discovery dial budget is capped by PEERDRIVE_MAX_PEERS.
// Discovery background (interconnect layer): the presence room makes "any node can discover any
// node" true, so if discovery means dialing immediately, a growing node count degrades into an
// O(n²) full mesh (one WebRTC connection per pair); the existing but never-used
// PEERDRIVE_MAX_PEERS holds the upper bound. A few easy-to-miss points are locked down separately:
//   - "local" (a browser's local WS session connected straight to this node) is not a peer
//     node and must not consume budget;
//   - unset / <=0 means unlimited; the default zero value must not shut interconnect down.
func TestDiscoveryDialAllowed_MaxPeers(t *testing.T) {
	newWithConns := func(maxPeers int, ids ...string) *PeerJSService {
		svc := newTestPeerJSService(t)
		svc.cfg.MaxPeers = maxPeers
		for _, id := range ids {
			svc.conns[id] = &fakeSession{id: id}
		}
		return svc
	}

	t.Run("reject after reaching limit", func(t *testing.T) {
		svc := newWithConns(2, "a", "b")
		assert.False(t, svc.discoveryDialAllowed())
	})

	t.Run("allow when under limit", func(t *testing.T) {
		svc := newWithConns(2, "a")
		assert.True(t, svc.discoveryDialAllowed())
	})

	t.Run("local session does not consume budget", func(t *testing.T) {
		svc := newWithConns(1, "local")
		assert.True(t, svc.discoveryDialAllowed(), "local WS session is not a peer node")
	})

	t.Run("when no limit is configured, treated as unlimited", func(t *testing.T) {
		svc := newWithConns(0, "a", "b", "c", "d", "e", "f", "g", "h", "i")
		assert.True(t, svc.discoveryDialAllowed())
	})

	t.Run("negative limit treated as unlimited", func(t *testing.T) {
		svc := newWithConns(-1, "a", "b", "c")
		assert.True(t, svc.discoveryDialAllowed())
	})

	t.Run("with limit 1, can still dial one peer after local", func(t *testing.T) {
		svc := newWithConns(1, "local")
		assert.True(t, svc.discoveryDialAllowed())
		svc.conns["real"] = &fakeSession{id: "real"}
		assert.False(t, svc.discoveryDialAllowed())
	})

	t.Run("in-flight connecting dials consume discovery budget (Issue #283)", func(t *testing.T) {
		svc := newWithConns(2, "a")
		assert.True(t, svc.discoveryDialAllowed())
		svc.connecting["dialing-b"] = struct{}{}
		assert.False(t, svc.discoveryDialAllowed(), "in-flight connecting dial must consume budget")

		// tryReserveDial for discovery also rejects when budget is exhausted
		assert.False(t, svc.tryReserveDial("dialing-c", true))
		// but non-discovery static dials are allowed
		assert.True(t, svc.tryReserveDial("static-c", false))
	})
}

// TestMaxPeers_ZeroMeansUnlimited explicitly locks the maxPeers fallback semantics (<=0 ⇒ a huge value).
func TestMaxPeers_ZeroMeansUnlimited(t *testing.T) {
	svc := newTestPeerJSService(t)
	svc.cfg.MaxPeers = 0
	assert.Greater(t, svc.maxPeers(), 1<<20, "0 must be interpreted as unlimited, not 0 peers")

	svc.cfg.MaxPeers = 3
	assert.Equal(t, 3, svc.maxPeers())
}
