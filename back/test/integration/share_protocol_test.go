//go:build integration

package integration

// share_protocol_test.go: **Protocol contract** test for share manifest frames (doc/NETDISK.md M2/M5).
//
// Why use Go↔Go to verify the JS client's contract: pure WebRTC consumer
// (packages/peerdrive-client, M5) cannot run in a real browser in CI, but its
// agreement with nodes is just a few frame types + field names. Here we use the **exact same frame sequence**
// (share → share-resp → req → meta/data/done) to run through two real nodes,
// locking in field names and semantics; the JS side then unit-tests against the same set of field names. Both sides together form interoperability evidence.
//
// Discovery context (netdisk target): Users want "after joining a node, see file links (packaged collections
// or individual files), select and save to download from others." "See file links" depends on the share
// frame distributing the collection's entries (path+hash) together — only giving the collection name, users can't click in.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"peerdrive/internal/config"
	"peerdrive/internal/model"
	"peerdrive/internal/repository"
	"peerdrive/internal/service"
	"peerdrive/internal/transport"
)

// TestShareProtocolContract Explicitly share a public collection → peer gets
// an exactly identical manifest via share frame, and can directly pull content using the hash from entries
// (the protocol foundation of "select and save").
func TestShareProtocolContract(t *testing.T) {
	requireInitDB(t)

	// Anonymous collection storage directory is package-level global (repository.SetAnonStorageDir); in single-process
	// dual-node tests A/B share it — doesn't affect this test: only A declares sharing.
	anonDir := t.TempDir()
	repository.SetAnonStorageDir(anonDir)

	content := []byte("contract-collection-content")
	// First put content into content-addressed storage (collection entry's hash is its sha256)
	fileHash := writeTestFile(t, anonDir, content)

	cfgA := config.Load()
	anonReader := service.NewAnonService(cfgA)
	collHash, err := anonReader.CreateCollection("contract collection", []model.AnonCollectionEntry{
		{Path: "docs/readme.txt", Providers: []model.Provider{{Type: "sha256", Value: fileHash, MimeType: "text/plain"}}},
	}, []string{"contract"})
	require.NoError(t, err, "failed to create collection")

	// A: enable sharing, explicitly declare this public collection
	cfgA.PeerJSEnable = true
	cfgA.PeerJSID = randID("cshare-a")
	cfgA.PeerJSHost, cfgA.PeerJSPort = splitHostPort(selfHostedURL)
	cfgA.PeerJSSecure = false
	cfgA.PeerJSKey = "testkey"
	cfgA.BTDHTEnabled = false
	cfgA.DiscoverURL = selfHostedURL
	cfgA.DiscoverPresence = true
	cfgA.MQTTCollections = ""
	cfgA.ShareEnable = true
	cfgA.ShareCollections = collHash
	svcA := transport.NewPeerJSService(cfgA, anonDir)
	shareSvc := service.NewNodeShare(cfgA, "") // "" = in-memory mode (integration tests don't persist to disk)
	shareSvc.SetAnonAccess(anonReader.GetCollectionByHash, anonReader.ListCollections)
	svcA.SetShareProvider(shareSvc.SnapshotFor)
	svcA.Start()
	t.Cleanup(svcA.Close)

	// Local self-check: snapshot parsing must hit this collection (otherwise subsequent assertions will misjudge as "peer didn't reply")
	snap := shareSvc.Snapshot()
	require.Len(t, snap.Collections, 1, "local share snapshot should contain 1 collection: %+v", snap)
	require.Equal(t, 0, len(snap.Files), "no shared dir configured -> should have no single files")

	// B: sharing not enabled, only as consumer
	cfgB := config.Load()
	cfgB.PeerJSEnable = true
	cfgB.PeerJSID = randID("cshare-b")
	cfgB.PeerJSHost, cfgB.PeerJSPort = splitHostPort(selfHostedURL)
	cfgB.PeerJSSecure = false
	cfgB.PeerJSKey = "testkey"
	cfgB.BTDHTEnabled = false
	cfgB.DiscoverURL = selfHostedURL
	cfgB.DiscoverPresence = true
	cfgB.MQTTCollections = ""
	svcB := transport.NewPeerJSService(cfgB, t.TempDir())
	svcB.Start()
	t.Cleanup(svcB.Close)

	waitConnections(t, svcB, map[string]bool{svcA.ID(): true}, 60*time.Second)

	// ── Contract assertion 1: manifest pulled via share frame ──
	remote, err := svcB.RequestShares(svcA.ID())
	require.NoError(t, err, "share frame request failed")
	require.Len(t, remote.Collections, 1, "peer share manifest should contain 1 collection")
	got := remote.Collections[0]
	require.Equal(t, collHash, got.Hash)
	require.Equal(t, "contract collection", got.Name, "friendly_name not sent with share")
	require.Equal(t, int64(1), got.Size, "size semantics = entry count")
	require.Len(t, got.Entries, 1, "collection entries must be sent with share (otherwise client cannot click in)")
	require.Equal(t, "docs/readme.txt", got.Entries[0].Path)
	require.Equal(t, fileHash, got.Entries[0].Hash)
	require.Equal(t, "text/plain", got.Entries[0].Mime)
	require.Empty(t, remote.Files, "no shared dir configured -> single-file manifest is empty")
	// Empty manifest must be an empty slice, not nil (frontend directly .map, doesn't accept null)
	require.NotNil(t, remote.Collections)
	require.NotNil(t, remote.Files)

	// ── Contract assertion 2: directly pull content using hash from manifest (first step of "select → save") ──
	data, err := svcB.FetchFromPeer(svcA.ID(), got.Entries[0].Hash, 0, -1)
	require.NoError(t, err, "pull by hash from share manifest failed")
	require.Equal(t, string(content), string(data), "pulled content does not match source")

	// ── Contract assertion 3: node without sharing enabled returns empty manifest (not err) ──
	empty, err := svcA.RequestShares(svcB.ID())
	require.NoError(t, err, "when peer has sharing disabled, share should return an empty manifest successfully")
	require.Empty(t, empty.Collections)
	require.Empty(t, empty.Files)
}
