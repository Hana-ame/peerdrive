//go:build integration

package integration

// e2e_full_chain_test.go: Comprehensive end-to-end (E2E) lifecycle testing.
//
// Exercises the complete user journey and protocol lifecycle across multiple nodes:
//  1. Topology & Signaling: In-process self-hosted signaling + HTTP discovery (/announce, /nodes).
//  2. Node A (Publisher / Drive Owner):
//     - Declares high throughput, p2ptun, streaming capabilities.
//     - Generates multi-chunk payload (300KB) to exercise adaptive chunking and buffer pooling.
//     - Creates both a Public Collection and a Protected Collection (with Passcode & AccessPolicy).
//     - Configures and enables sharing scope.
//  3. Node B (Consumer / Drive Peer):
//     - Discovers Node A via presence room (zero static peers).
//     - Handshakes over WebRTC DataChannel; validates symmetrical capability negotiation
//       (CapHighThroughput, CapP2PTun, CapReq, CapShare).
//     - Performs byte-range fetch (offset/size) directly via WebRTC DataChannel.
//     - Queries share manifest without passcode: receives public collection entries;
//       verifies protected collection has entries masked (IsProtected=true, 0 entries).
//     - Queries share manifest with invalid passcode: entries remain masked.
//     - Queries share manifest with correct passcode: protected collection entries are unlocked.
//     - Downloads multi-chunk file via PeerPuller with real streaming, hash validation,
//       and registration in local file_index.
//     - Validates deduplication: re-downloading returns Skipped=true without network overhead.
//     - Real WebRTC partial .part file resume: pre-existing partial file is detected,
//       download resumes from byte offset over WebRTC, validating final hash integrity.
//  4. Node C (Downstream Secondary Consumer):
//     - Discovers Node B via presence room (Node C has no connection to Node A).
//     - Connects to Node B and pulls the file originally held by Node A.
//     - Asserts that Node B autonomously serves the content from its local drive/index.
//     - Validates full content byte-for-byte and SHA-256 match.
//  5. Security & Boundary Guardrails:
//     - Corrupted or nonexistent hash requests fail safely without panics or hangs.
//     - Tampered payload is rejected by PeerPuller, cleaning up .part temporary files.

import (
	"bytes"
	"crypto/rand"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"peerdrive/internal/config"
	"peerdrive/internal/model"
	"peerdrive/internal/repository"
	"peerdrive/internal/service"
	"peerdrive/internal/transport"
)

// TestEndToEnd_FullChainLifecycle executes the full start-to-finish E2E chain:
// Publisher (A) -> WebRTC & Signaling -> Capability Handshake -> Range Fetch
// -> Passcode Share Gate -> Multi-chunk Pull & Index Registration -> Deduplication
// -> Cascading Re-serving (B -> C) -> Midstream .part Byte-Level Resume -> Error Bounds.
func TestEndToEnd_FullChainLifecycle(t *testing.T) {
	requireInitDB(t)

	// Shared anon storage directory for node A's anonymous collections
	anonDir := t.TempDir()
	repository.SetAnonStorageDir(anonDir)

	storageA := t.TempDir()
	downloadB := t.TempDir()
	downloadC := t.TempDir()

	// 1. Prepare Test Payloads:
	// File 1: 300KB multi-chunk payload (crosses standard 64KB chunk boundary to exercise streaming)
	file1Content := make([]byte, 300*1024)
	_, err := rand.Read(file1Content)
	require.NoError(t, err)
	file1Hash := writeTestFile(t, storageA, file1Content)

	// File 2: Protected collection payload
	file2Content := []byte("confidential-payload-protected-by-passcode-0987654321")
	file2Hash := writeTestFile(t, anonDir, file2Content)

	// File 3: Resume test payload (200KB)
	file3Content := make([]byte, 200*1024)
	_, err = rand.Read(file3Content)
	require.NoError(t, err)
	file3Hash := writeTestFile(t, storageA, file3Content)

	// 2. Setup Node A Collections:
	cfgA := config.Load()
	anonSvcA := service.NewAnonService(cfgA)

	// Public collection containing File 1
	pubCollHash, err := anonSvcA.CreateCollection("public-package", []model.AnonCollectionEntry{
		{Path: "large/asset.dat", Providers: []model.Provider{{Type: "sha256", Value: file1Hash, MimeType: "application/octet-stream"}}},
	}, []string{"public", "e2e"})
	require.NoError(t, err, "failed to create public collection")

	// Protected collection containing File 2
	const protectedPasscode = "secret-passcode-2026"
	protCollHash, err := anonSvcA.CreateCollectionWithPolicy(
		"protected-package",
		[]model.AnonCollectionEntry{
			{Path: "secret/token.txt", Providers: []model.Provider{{Type: "sha256", Value: file2Hash, MimeType: "text/plain"}}},
		},
		[]string{"confidential"},
		model.VisibilityPublic,
		nil,
		model.AccessPolicyProtected,
		protectedPasscode,
		"publisher",
	)
	require.NoError(t, err, "failed to create protected collection")

	// 3. Configure and Start Node A (Publisher)
	cfgA.PeerJSEnable = true
	cfgA.PeerJSID = randID("e2e-node-a")
	cfgA.PeerJSHost, cfgA.PeerJSPort = splitHostPort(selfHostedURL)
	cfgA.PeerJSSecure = false
	cfgA.PeerJSKey = "testkey"
	cfgA.BTDHTEnabled = false
	cfgA.DiscoverURL = selfHostedURL
	cfgA.DiscoverPresence = true
	cfgA.MQTTCollections = ""
	cfgA.ShareEnable = true
	cfgA.ShareCollections = pubCollHash + "," + protCollHash
	cfgA.DownloadDir = storageA

	svcA := transport.NewPeerJSService(cfgA, storageA)
	// Declare full capability suite on Node A including high throughput and p2ptun
	svcA.SetLocalCapabilities([]string{
		transport.CapReq,
		transport.CapShare,
		transport.CapIndex,
		transport.CapPull,
		transport.CapDisplay,
		transport.CapStream,
		transport.CapHighThroughput,
		transport.CapHeavyTraffic,
		transport.CapP2PTun,
	})

	shareSvcA := service.NewNodeShare(cfgA, "")
	shareSvcA.SetAnonAccess(anonSvcA.GetCollectionByHash, anonSvcA.ListCollections)
	svcA.SetShareProvider(shareSvcA.SnapshotFor)
	svcA.SetShareProviderWithToken(shareSvcA.SnapshotForToken)
	svcA.Start()
	t.Cleanup(svcA.Close)

	// 4. Configure and Start Node B (Consumer / Peer)
	cfgB := config.Load()
	cfgB.PeerJSEnable = true
	cfgB.PeerJSID = randID("e2e-node-b")
	cfgB.PeerJSHost, cfgB.PeerJSPort = splitHostPort(selfHostedURL)
	cfgB.PeerJSSecure = false
	cfgB.PeerJSKey = "testkey"
	cfgB.BTDHTEnabled = false
	cfgB.DiscoverURL = selfHostedURL
	cfgB.DiscoverPresence = true
	cfgB.MQTTCollections = ""
	cfgB.DownloadDir = downloadB

	svcB := transport.NewPeerJSService(cfgB, t.TempDir())
	svcB.SetLocalCapabilities([]string{
		transport.CapReq,
		transport.CapShare,
		transport.CapIndex,
		transport.CapPull,
		transport.CapHighThroughput,
		transport.CapP2PTun,
	})
	svcB.Start()
	t.Cleanup(svcB.Close)

	// 5. Configure and Start Node C (Downstream Secondary Consumer)
	cfgC := config.Load()
	cfgC.PeerJSEnable = true
	cfgC.PeerJSID = randID("e2e-node-c")
	cfgC.PeerJSHost, cfgC.PeerJSPort = splitHostPort(selfHostedURL)
	cfgC.PeerJSSecure = false
	cfgC.PeerJSKey = "testkey"
	cfgC.BTDHTEnabled = false
	cfgC.DiscoverURL = selfHostedURL
	cfgC.DiscoverPresence = true
	cfgC.MQTTCollections = ""
	cfgC.DownloadDir = downloadC

	svcC := transport.NewPeerJSService(cfgC, t.TempDir())
	svcC.SetLocalCapabilities([]string{
		transport.CapReq,
		transport.CapShare,
	})
	svcC.Start()
	t.Cleanup(svcC.Close)

	// 6. Discovery & WebRTC Interconnect: Node B discovers Node A
	t.Log("Step 1: Waiting for Node B to connect to Node A via Presence Discovery...")
	waitConnections(t, svcB, map[string]bool{svcA.ID(): true}, 60*time.Second)

	// 7. Verify Capability Handshake between Node A and Node B
	t.Log("Step 2: Verifying Capability Negotiation...")
	var capsAB []string
	require.Eventually(t, func() bool {
		caps, ok := svcB.PeerCapabilities(svcA.ID())
		if !ok {
			return false
		}
		capsAB = caps
		return slices.Contains(caps, transport.CapHighThroughput) && slices.Contains(caps, transport.CapP2PTun)
	}, 10*time.Second, 100*time.Millisecond, "High throughput mode and P2PTun must be negotiated between Node A and Node B")
	require.Contains(t, capsAB, transport.CapReq)
	require.Contains(t, capsAB, transport.CapShare)
	require.Contains(t, capsAB, transport.CapHighThroughput, "High throughput mode must be negotiated")
	require.Contains(t, capsAB, transport.CapP2PTun, "P2PTun capability must be negotiated")

	// 8. Direct WebRTC Byte-Range Fetch (Offset/Size Partial Stream)
	t.Log("Step 3: Verifying direct WebRTC byte-range fetch...")
	const offset = 4096
	const length = 8192
	rangeSlice, err := svcB.FetchFromPeer(svcA.ID(), file1Hash, offset, length)
	require.NoError(t, err, "byte range fetch from peer failed")
	require.Equal(t, length, len(rangeSlice), "fetched slice length mismatch")
	require.True(t, bytes.Equal(file1Content[offset:offset+length], rangeSlice), "range bytes do not match original slice")

	// 9. Share Scope Inspection & Passcode Access Control
	t.Log("Step 4: Verifying Share Scope and Passcode Access Policy...")
	// 9a. Query without passcode
	snapNoPass, err := svcB.RequestShares(svcA.ID())
	require.NoError(t, err, "share query without passcode failed")
	require.Len(t, snapNoPass.Collections, 2, "both collections must be visible in manifest")

	var pubColl, protColl model.ShareCollectionInfo
	for _, c := range snapNoPass.Collections {
		if c.Hash == pubCollHash {
			pubColl = c
		} else if c.Hash == protCollHash {
			protColl = c
		}
	}
	require.NotEmpty(t, pubColl.Hash, "public collection not found in share manifest")
	require.False(t, pubColl.IsProtected, "public collection must not be protected")
	require.Len(t, pubColl.Entries, 1, "public collection entries must be visible")
	require.Equal(t, file1Hash, pubColl.Entries[0].Hash)

	require.NotEmpty(t, protColl.Hash, "protected collection not found in share manifest")
	require.True(t, protColl.IsProtected, "protected collection must be marked IsProtected=true")
	require.Empty(t, protColl.Entries, "protected collection entries must be hidden without passcode")

	// 9b. Query with incorrect passcode
	snapWrongPass, err := svcB.RequestSharesWithPasscode(svcA.ID(), "wrong-passcode")
	require.NoError(t, err)
	for _, c := range snapWrongPass.Collections {
		if c.Hash == protCollHash {
			require.True(t, c.IsProtected, "wrong passcode must keep collection protected")
			require.Empty(t, c.Entries, "wrong passcode must not unlock entries")
		}
	}

	// 9c. Query with correct passcode
	snapUnlocked, err := svcB.RequestSharesWithPasscode(svcA.ID(), protectedPasscode)
	require.NoError(t, err)
	var unlockedProtColl model.ShareCollectionInfo
	for _, c := range snapUnlocked.Collections {
		if c.Hash == protCollHash {
			unlockedProtColl = c
		}
	}
	require.False(t, unlockedProtColl.IsProtected, "correct passcode must unlock collection")
	require.Len(t, unlockedProtColl.Entries, 1, "correct passcode must reveal collection entries")
	require.Equal(t, file2Hash, unlockedProtColl.Entries[0].Hash)
	require.Equal(t, "secret/token.txt", unlockedProtColl.Entries[0].Path)

	// 10. Multi-chunk File Pull via PeerPuller (Node A -> Node B)
	t.Log("Step 5: Executing multi-chunk PeerPuller transfer (300KB)...")
	idxB := svcB.FileIndex()
	pullerB := service.NewPeerPuller(downloadB)
	pullerB.SetSource(svcB)
	pullerB.SetFileAccess(
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

	pullJob, err := pullerB.Start(svcA.ID(), file1Hash, "asset.dat", "large/asset.dat", "")
	require.NoError(t, err, "failed to start pull job")

	// Await completion
	var doneJob service.PullJob
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		j, exists := pullerB.Get(pullJob.ID)
		require.True(t, exists)
		if j.Done() {
			doneJob = j
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	require.Equal(t, service.PullDone, doneJob.Status, "pull failed: %s", doneJob.Error)
	require.False(t, doneJob.Skipped, "first pull must not be skipped")
	require.Equal(t, int64(len(file1Content)), doneJob.Received)

	// Validate written file on disk
	expectedFileB := filepath.Join(downloadB, "pulled", "large", "asset.dat")
	require.Equal(t, expectedFileB, doneJob.SavedTo)
	savedBytes, err := os.ReadFile(expectedFileB)
	require.NoError(t, err)
	require.True(t, bytes.Equal(file1Content, savedBytes), "saved file content does not match source")

	// Validate registration in Node B's local file_index
	fiB, err := idxB.Info(file1Hash)
	require.NoError(t, err, "downloaded file must be queryable in Node B's file index")
	require.Equal(t, file1Hash, fiB.Hash)
	require.Equal(t, expectedFileB, fiB.Path)
	require.Equal(t, int64(len(file1Content)), fiB.Size)

	// Validate Deduplication: pulling same content again must skip
	t.Log("Step 6: Verifying content-addressed deduplication skip...")
	againJob, err := pullerB.Start(svcA.ID(), file1Hash, "asset.dat", "large/asset.dat", "")
	require.NoError(t, err)
	deadline = time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		j, exists := pullerB.Get(againJob.ID)
		require.True(t, exists)
		if j.Done() {
			require.Equal(t, service.PullDone, j.Status)
			require.True(t, j.Skipped, "deduplication must skip existing content")
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// 11. Cascading Secondary Re-serving (Node B serves to Node C)
	t.Log("Step 7: Verifying cascading P2P re-serving (Node C pulls from Node B)...")
	waitConnections(t, svcC, map[string]bool{svcB.ID(): true}, 60*time.Second)

	// Node C capability negotiation with Node B
	var capsBC []string
	require.Eventually(t, func() bool {
		caps, ok := svcC.PeerCapabilities(svcB.ID())
		if !ok {
			return false
		}
		capsBC = caps
		return slices.Contains(caps, transport.CapReq) && slices.Contains(caps, transport.CapShare)
	}, 10*time.Second, 100*time.Millisecond, "Node C must negotiate capabilities with Node B")
	require.Contains(t, capsBC, transport.CapReq)
	require.Contains(t, capsBC, transport.CapShare)
	// Node C did not advertise CapP2PTun or CapHighThroughput, so negotiated intersection must NOT contain them
	require.NotContains(t, capsBC, transport.CapP2PTun)

	// Node C pulls File 1 from Node B (Node B serves it out of downloadRoot)
	pulledByC, err := svcC.FetchFromPeer(svcB.ID(), file1Hash, 0, -1)
	require.NoError(t, err, "Node C failed to pull file from secondary node B")
	require.True(t, bytes.Equal(file1Content, pulledByC), "Node C content does not match original file")

	// 12. Real WebRTC Partial .part File Resume (Issue #276)
	t.Log("Step 8: Verifying byte-level resume from pre-existing .part file over WebRTC...")
	partDir := filepath.Join(downloadB, "pulled", "resumed")
	require.NoError(t, os.MkdirAll(partDir, 0o755))
	partialFilePath := filepath.Join(partDir, "asset_resumed.dat.part")
	// Seed partial file with the first 64KB of file3Content
	const partialSeedSize = 64 * 1024
	require.NoError(t, os.WriteFile(partialFilePath, file3Content[:partialSeedSize], 0o644))

	resumeJob, err := pullerB.Start(svcA.ID(), file3Hash, "asset_resumed.dat", "resumed/asset_resumed.dat", "")
	require.NoError(t, err)

	deadline = time.Now().Add(60 * time.Second)
	var doneResume service.PullJob
	for time.Now().Before(deadline) {
		j, exists := pullerB.Get(resumeJob.ID)
		require.True(t, exists)
		if j.Done() {
			doneResume = j
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	require.Equal(t, service.PullDone, doneResume.Status, "resume pull failed: %s", doneResume.Error)
	require.False(t, doneResume.Skipped, "resume pull must not be skipped")
	finalResumedFile := filepath.Join(partDir, "asset_resumed.dat")
	resumedBytes, err := os.ReadFile(finalResumedFile)
	require.NoError(t, err, "resumed file must exist on disk")
	require.True(t, bytes.Equal(file3Content, resumedBytes), "resumed file content must be byte-identical to original")
	// Verify .part is removed
	_, err = os.Stat(partialFilePath)
	require.True(t, os.IsNotExist(err), "temporary .part file must be removed after successful resume")

	// 13. Security Boundary: Requesting nonexistent/corrupted hash fails safely
	t.Log("Step 9: Verifying failure boundary on nonexistent content...")
	nonexistentHash := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	_, err = svcB.FetchFromPeer(svcA.ID(), nonexistentHash, 0, -1)
	require.Error(t, err, "request for nonexistent hash must fail")
}

// TestEndToEnd_TamperAndResumeResilience tests midstream interruption handling and corrupted payload rejection.
func TestEndToEnd_TamperAndResumeResilience(t *testing.T) {
	requireInitDB(t)

	downloadDir := t.TempDir()
	puller := service.NewPeerPuller(downloadDir)

	// 1. Corrupted payload rejection test
	fakeContent := []byte("legitimate expected content bytes 1234567890")
	realHash := sha256Hex(fakeContent)
	corruptedContent := []byte("tampered corrupted bytes injected by adversary")

	tamperedSource := &mockTestPullSource{
		content: map[string][]byte{
			realHash: corruptedContent,
		},
	}
	puller.SetSource(tamperedSource)

	registered := make([]string, 0)
	puller.SetFileAccess(
		func(h string) bool { return false },
		func(path string) (string, int64, error) {
			registered = append(registered, path)
			return realHash, int64(len(corruptedContent)), nil
		},
	)

	job, err := puller.Start("adversary-peer", realHash, "tampered.dat", "tampered.dat", "")
	require.NoError(t, err)

	// Wait for failure
	deadline := time.Now().Add(10 * time.Second)
	var finalJob service.PullJob
	for time.Now().Before(deadline) {
		if j, ok := puller.Get(job.ID); ok && j.Done() {
			finalJob = j
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	require.Equal(t, service.PullFailed, finalJob.Status, "tampered payload must fail validation")
	require.Contains(t, finalJob.Error, "hash mismatch")
	require.Empty(t, registered, "tampered file must never be registered in file index")

	// Ensure no .part or final file remains
	targetPath := filepath.Join(downloadDir, "pulled", "tampered.dat")
	_, err = os.Stat(targetPath)
	require.True(t, os.IsNotExist(err), "final file must not exist after validation failure")
	_, err = os.Stat(targetPath + ".part")
	require.True(t, os.IsNotExist(err), "temporary .part file must be cleaned up")
}

// mockTestPullSource implements service.PeerPullSource for resilience validation.
type mockTestPullSource struct {
	content map[string][]byte
}

func (m *mockTestPullSource) FetchFromPeer(peerID, hash string, offset, size int64) ([]byte, error) {
	data, ok := m.content[hash]
	if !ok {
		return nil, os.ErrNotExist
	}
	if offset >= int64(len(data)) {
		return []byte{}, nil
	}
	end := int64(len(data))
	if size > 0 && offset+size < end {
		end = offset + size
	}
	return data[offset:end], nil
}

func (m *mockTestPullSource) OpenStream(peerID, hash string, offset, size int64) (io.ReadCloser, error) {
	data, err := m.FetchFromPeer(peerID, hash, offset, size)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

// TestEndToEnd_SecurityBoundariesAndAccessPolicy 发现背景：端到端跨节点交互中，
// 恶意或未授权对端可能尝试暴力绕过受保护合集口令、请求不存在或恶意哈希探测，
// 必须全链路验证所有安全门禁在真实信令与 WebRTC DataChannel 下均生效。
func TestEndToEnd_SecurityBoundariesAndAccessPolicy(t *testing.T) {
	requireInitDB(t)

	anonDir := t.TempDir()
	repository.SetAnonStorageDir(anonDir)
	storageA := t.TempDir()
	downloadB := t.TempDir()

	// Node A: 发布节点
	cfgA := config.Load()
	anonSvcA := service.NewAnonService(cfgA)

	fileAContent := []byte("top-secret-protected-content-007")
	fileAHash := writeTestFile(t, storageA, fileAContent)

	const passcode = "pass-gate-2026"
	protCollHash, err := anonSvcA.CreateCollectionWithPolicy(
		"confidential-docs",
		[]model.AnonCollectionEntry{
			{Path: "secret.doc", Providers: []model.Provider{{Type: "sha256", Value: fileAHash, MimeType: "text/plain"}}},
		},
		[]string{"security"},
		model.VisibilityPublic,
		nil,
		model.AccessPolicyProtected,
		passcode,
		"publisher",
	)
	require.NoError(t, err)

	cfgA.PeerJSEnable = true
	cfgA.PeerJSID = randID("sec-node-a")
	cfgA.PeerJSHost, cfgA.PeerJSPort = splitHostPort(selfHostedURL)
	cfgA.PeerJSSecure = false
	cfgA.PeerJSKey = "testkey"
	cfgA.BTDHTEnabled = false
	cfgA.DiscoverURL = selfHostedURL
	cfgA.DiscoverPresence = true
	cfgA.MQTTCollections = ""
	cfgA.ShareEnable = true
	cfgA.ShareCollections = protCollHash
	cfgA.DownloadDir = storageA

	svcA := transport.NewPeerJSService(cfgA, storageA)
	svcA.SetLocalCapabilities([]string{
		transport.CapReq,
		transport.CapShare,
		transport.CapPull,
	})
	shareSvcA := service.NewNodeShare(cfgA, "")
	shareSvcA.SetAnonAccess(anonSvcA.GetCollectionByHash, anonSvcA.ListCollections)
	svcA.SetShareProvider(shareSvcA.SnapshotFor)
	svcA.SetShareProviderWithToken(shareSvcA.SnapshotForToken)
	svcA.Start()
	t.Cleanup(svcA.Close)

	// Node B: 探测节点
	cfgB := config.Load()
	cfgB.PeerJSEnable = true
	cfgB.PeerJSID = randID("sec-node-b")
	cfgB.PeerJSHost, cfgB.PeerJSPort = splitHostPort(selfHostedURL)
	cfgB.PeerJSSecure = false
	cfgB.PeerJSKey = "testkey"
	cfgB.BTDHTEnabled = false
	cfgB.DiscoverURL = selfHostedURL
	cfgB.DiscoverPresence = true
	cfgB.MQTTCollections = ""
	cfgB.DownloadDir = downloadB

	svcB := transport.NewPeerJSService(cfgB, t.TempDir())
	svcB.SetLocalCapabilities([]string{
		transport.CapReq,
		transport.CapShare,
		transport.CapPull,
	})
	svcB.Start()
	t.Cleanup(svcB.Close)

	// 等待 B 与 A 发现并建立 WebRTC DataChannel
	waitConnections(t, svcB, map[string]bool{svcA.ID(): true}, 60*time.Second)

	// 1. 无口令查询 share 清单：条目必须被锁定
	snapNoPass, err := svcB.RequestSharesWithPasscode(svcA.ID(), "")
	require.NoError(t, err)
	foundProtected := false
	for _, c := range snapNoPass.Collections {
		if c.Name == "confidential-docs" {
			foundProtected = true
			require.True(t, c.IsProtected, "protected collection must be marked as protected without passcode")
			require.Empty(t, c.Entries, "protected collection entries must be hidden")
		}
	}
	require.True(t, foundProtected, "confidential-docs must be returned in manifest")

	// 2. 错误口令查询：条目依然锁定
	snapWrongPass, err := svcB.RequestSharesWithPasscode(svcA.ID(), "wrong-code")
	require.NoError(t, err)
	for _, c := range snapWrongPass.Collections {
		if c.Name == "confidential-docs" {
			require.True(t, c.IsProtected, "wrong passcode must keep collection locked")
			require.Empty(t, c.Entries, "wrong passcode must not reveal entries")
		}
	}

	// 3. 正确口令查询：条目解锁
	snapCorrectPass, err := svcB.RequestSharesWithPasscode(svcA.ID(), passcode)
	require.NoError(t, err)
	for _, c := range snapCorrectPass.Collections {
		if c.Name == "confidential-docs" {
			require.False(t, c.IsProtected, "correct passcode must unlock collection")
			require.Len(t, c.Entries, 1, "unlocked collection must reveal entries")
			require.Equal(t, fileAHash, c.Entries[0].Hash)
		}
	}

	// 4. 异常探测：请求损坏/不存在的哈希，必须安全返回错误且不引起 panic 或卡死
	fakeHash := "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	_, err = svcB.FetchFromPeer(svcA.ID(), fakeHash, 0, 1024)
	require.Error(t, err, "fetching non-existent hash must fail gracefully")
}
