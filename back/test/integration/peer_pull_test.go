//go:build integration

package integration

// peer_pull_test.go: Cross-node pull-and-save end-to-end (doc/NETDISK.md M3).
//
// Covers the user's primary requirement: "select a file and save it to download from others" — A holds the content
// (content-addressed storage only, no other index), B pulls the content over real WebRTC, verifies sha256,
// writes to disk, and registers it in the local file index (from then on B's "My Files" can see it,
// and B can serve it to a third node).
//
// Discovery context (netdisk target): Previously only FetchFromPeer (whole package in memory, 64MB HTTP limit)
// and source.p2p on-demand origin fetch (no disk write). "Save to my netdisk" requires streaming disk write + content
// verification + index registration all together; missing any one would make the user feel "downloaded but can't find the file."

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"peerdrive/internal/config"
	"peerdrive/internal/service"
	"peerdrive/internal/transport"
)

// TestPeerPullSavesToLocalDrive A holds content → B pulls and saves → disk write + registration + re-serveable.
func TestPeerPullSavesToLocalDrive(t *testing.T) {
	requireInitDB(t)

	storageA := t.TempDir()
	content := []byte("peer-pull-e2e-content-0123456789")
	hash := writeTestFile(t, storageA, content)

	newNode := func(id, storage, downloadDir string) *transport.PeerJSService {
		requireInitDB(t)
		cfg := config.Load()
		cfg.PeerJSEnable = true
		cfg.PeerJSID = id
		cfg.PeerJSHost, cfg.PeerJSPort = splitHostPort(selfHostedURL)
		cfg.PeerJSSecure = false
		cfg.PeerJSKey = "testkey"
		cfg.BTDHTEnabled = false
		cfg.DiscoverURL = selfHostedURL
		cfg.DiscoverPresence = true
		cfg.MQTTCollections = ""
		// downloadDir must be explicitly specified: the node's internal file_index uses it as
		// the "allowed root directory" (H2 security boundary); the save directory must fall within the same root,
		// otherwise registration is rejected → serveFile falls back to CAS → saved file cannot be served.
		if downloadDir != "" {
			cfg.DownloadDir = downloadDir
		}
		svc := transport.NewPeerJSService(cfg, storage)
		svc.Start()
		t.Cleanup(svc.Close)
		return svc
	}

	svcA := newNode(randID("pull-a"), storageA, "")
	// B's save directory = its file_index allowed root directory (same as cfg.DownloadDir in production deployments)
	downloadRoot := t.TempDir()
	svcB := newNode(randID("pull-b"), t.TempDir(), downloadRoot)
	// C is created in advance: requireInitDB resets the global in-memory DB (see integration_test.go),
	// creating a node after pull completion would wipe B's index registration — that would cause
	// the "save then continue serving" assertion to fail due to test side effects, not product behavior.
	svcC := newNode(randID("pull-c"), t.TempDir(), "")

	waitConnections(t, svcB, map[string]bool{svcA.ID(): true}, 60*time.Second)

	// B-side pull service: reuses the node's own file_index (real chain, no test stubs)
	idxB := svcB.FileIndex()
	puller := service.NewPeerPuller(downloadRoot)
	puller.SetSource(svcB)
	puller.SetFileAccess(
		func(h string) bool {
			fi, err := idxB.Info(h)
			return err == nil && fi != nil && fi.Path != "" && fi.Size > 0
		},
		func(path string) (string, int64, error) {
			fi, err := idxB.Create(path)
			if err != nil {
				return "", 0, err
			}
			return fi.Hash, fi.Size, nil
		},
	)

	job, err := puller.Start(svcA.ID(), hash, "saved.bin", "from-peer/saved.bin", "")
	require.NoError(t, err, "failed to start pull")

	// Wait for terminal state
	var done service.PullJob
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		j, ok := puller.Get(job.ID)
		require.True(t, ok)
		if j.Done() {
			done = j
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.Equal(t, service.PullDone, done.Status, "pull did not succeed: err=%s", done.Error)
	require.False(t, done.Skipped, "content not available locally; should not skip")

	// ① Disk write location preserves the relative structure provided by the peer
	want := filepath.Join(downloadRoot, "pulled", "from-peer", "saved.bin")
	require.Equal(t, want, done.SavedTo)
	got, err := os.ReadFile(want)
	require.NoError(t, err, "cannot read the saved file")
	require.Equal(t, string(content), string(got))
	require.Equal(t, int64(len(content)), done.Received)

	// ② Registered in local index ("My Files" visible), and queryable by hash
	fi, err := idxB.Info(hash)
	require.NoError(t, err, "info should be queryable after registration")
	require.Equal(t, hash, fi.Hash)
	require.Equal(t, want, fi.Path)

	// ③ "Save" the same content again → skip (content-addressed deduplication)
	again, err := puller.Start(svcA.ID(), hash, "saved.bin", "from-peer/saved.bin", "")
	require.NoError(t, err)
	deadline = time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if j, ok := puller.Get(again.ID); ok && j.Done() {
			require.Equal(t, service.PullDone, j.Status)
			require.True(t, j.Skipped, "same content already exists locally; should skip download")
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// ④ The saved file can be served by this node (B is now the holder).
	//    Verified using B's own inbound path: C pulls the same hash from B and should succeed.
	waitConnections(t, svcC, map[string]bool{svcB.ID(): true}, 60*time.Second)
	data, err := svcC.FetchFromPeer(svcB.ID(), hash, 0, -1)
	require.NoError(t, err, "after B saves, it should serve content to other nodes")
	require.Equal(t, string(content), string(data))
}
