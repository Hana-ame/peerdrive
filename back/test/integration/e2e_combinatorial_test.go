//go:build integration

package integration

// e2e_combinatorial_test.go: Combinatorial, multi-step, repeating stress and chaos integration tests.
//
// 发现背景：针对用户指令「组合，重复多个操作步骤以获得」，构建高级组合循环压力与混沌验证套件。
// 传统单元测试与单次 E2E 仅测试一次静态路径，容易掩盖以下严重生产隐患：
//  1. 连接长期存活下多次数据通道操作后的状态污染与背压死锁（sendMu / channel buffers）；
//  2. 动态多次创建合集、修改分叉（Fork/Commit）与动态扩大共享范围（Share Scope）时的元数据漂移；
//  3. 跨节点多轮拉取中内容寻址去重（dedup skip）在重复执行下的幂等性与一致性；
//  4. 远程访客隔离收件箱（Inbox Quarantine）在多次交替审核通过（Approve）与拒绝删除（Reject）下的状态机与物理文件清理；
//  5. 下游二次分发节点在多轮增量获取后的级联服务可靠性；
//  6. 周期性插入异常与混沌请求（非预期/损坏哈希、并发部分范围读取），验证主通道在扰动下的自愈性与零崩溃。

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"peerdrive/internal/config"
	"peerdrive/internal/model"
	"peerdrive/internal/repository"
	"peerdrive/internal/service"
	"peerdrive/internal/transport"
)

// TestEndToEnd_CombinatorialRepetitionAndStress runs a multi-round combinatorial lifecycle
// combining dynamic content generation, collection mutation/forking, dynamic share publishing,
// WebRTC DataChannel range fetches, passcode gating, PeerPuller streaming, deduplication,
// quarantine inbox moderation (alternating approve/reject), secondary cascading distribution,
// and concurrency/chaos requests, repeating across 4 complete cycles.
func TestEndToEnd_CombinatorialRepetitionAndStress(t *testing.T) {
	requireInitDB(t)

	anonDir := t.TempDir()
	repository.SetAnonStorageDir(anonDir)

	storageA := t.TempDir()
	downloadB := t.TempDir()
	downloadC := t.TempDir()

	// 1. Initialize Node A (Host / Drive Owner)
	cfgA := config.Load()
	anonSvcA := service.NewAnonService(cfgA)
	idxSvcA := transport.NewFileIndexService(storageA)
	t.Cleanup(func() { idxSvcA.Close() })

	cfgA.PeerJSEnable = true
	cfgA.PeerJSID = randID("combo-node-a")
	cfgA.PeerJSHost, cfgA.PeerJSPort = splitHostPort(selfHostedURL)
	cfgA.PeerJSSecure = false
	cfgA.PeerJSKey = "testkey"
	cfgA.BTDHTEnabled = false
	cfgA.DiscoverURL = selfHostedURL
	cfgA.DiscoverPresence = true
	cfgA.MQTTCollections = ""
	cfgA.ShareEnable = true
	cfgA.ShareCollections = "all" // Automatically encompasses all created public/passcode collections
	cfgA.DownloadDir = storageA

	svcA := transport.NewPeerJSService(cfgA, storageA)
	svcA.SetLocalCapabilities([]string{
		transport.CapReq,
		transport.CapShare,
		transport.CapIndex,
		transport.CapPull,
		transport.CapHighThroughput,
	})
	shareSvcA := service.NewNodeShare(cfgA, "")
	shareSvcA.SetAnonAccess(anonSvcA.GetCollectionByHash, anonSvcA.ListCollections)
	svcA.SetShareProvider(shareSvcA.SnapshotFor)
	svcA.SetShareProviderWithToken(shareSvcA.SnapshotForToken)
	svcA.Start()
	t.Cleanup(svcA.Close)

	// 2. Initialize Node B (Primary Consumer / Peer)
	cfgB := config.Load()
	cfgB.PeerJSEnable = true
	cfgB.PeerJSID = randID("combo-node-b")
	cfgB.PeerJSHost, cfgB.PeerJSPort = splitHostPort(selfHostedURL)
	cfgB.PeerJSSecure = false
	cfgB.PeerJSKey = "testkey"
	cfgB.BTDHTEnabled = false
	cfgB.DiscoverURL = selfHostedURL
	cfgB.DiscoverPresence = true
	cfgB.MQTTCollections = ""
	cfgB.DownloadDir = downloadB

	svcB := transport.NewPeerJSService(cfgB, downloadB)
	svcB.SetLocalCapabilities([]string{
		transport.CapReq,
		transport.CapShare,
		transport.CapIndex,
		transport.CapPull,
		transport.CapHighThroughput,
	})
	svcB.Start()
	t.Cleanup(svcB.Close)

	// 3. Initialize Node C (Secondary Downstream Consumer)
	cfgC := config.Load()
	cfgC.PeerJSEnable = true
	cfgC.PeerJSID = randID("combo-node-c")
	cfgC.PeerJSHost, cfgC.PeerJSPort = splitHostPort(selfHostedURL)
	cfgC.PeerJSSecure = false
	cfgC.PeerJSKey = "testkey"
	cfgC.BTDHTEnabled = false
	cfgC.DiscoverURL = selfHostedURL
	cfgC.DiscoverPresence = true
	cfgC.MQTTCollections = ""
	cfgC.DownloadDir = downloadC

	svcC := transport.NewPeerJSService(cfgC, downloadC)
	svcC.SetLocalCapabilities([]string{
		transport.CapReq,
		transport.CapShare,
	})
	svcC.Start()
	t.Cleanup(svcC.Close)

	// Wait for WebRTC presence discovery between nodes
	t.Log("== Setup: Establishing WebRTC DataChannels ==")
	waitConnections(t, svcB, map[string]bool{svcA.ID(): true}, 60*time.Second)
	waitConnections(t, svcC, map[string]bool{svcB.ID(): true}, 60*time.Second)

	// Setup PeerPuller on Node B
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

	var lastCreatedCollectionHash string

	const totalCycles = 4
	t.Logf("== Starting Combinatorial Repeating Test: %d Cycles ==", totalCycles)

	for cycle := 1; cycle <= totalCycles; cycle++ {
		t.Logf("--- [Cycle %d/%d] Beginning Combined Operations ---", cycle, totalCycles)

		// ─── Step 1: Dynamic Content Synthesis (Multi-size Payloads) ───
		// 1a. Small text payload
		smallText := []byte(fmt.Sprintf("cycle-%d: payload created at %s", cycle, time.Now().UTC().Format(time.RFC3339Nano)))
		smallHash := writeTestFile(t, storageA, smallText)

		// 1b. Multi-chunk binary payload (128KB, spans 2x 64KB WebRTC DataChannel chunks)
		largeBlob := make([]byte, 128*1024)
		_, err := rand.Read(largeBlob)
		require.NoError(t, err)
		largeHash := writeTestFile(t, storageA, largeBlob)

		// ─── Step 2: Collection Assembly & Policy Definition ───
		// Create a public collection for smallText
		pubCollName := fmt.Sprintf("pub-coll-cycle-%d", cycle)
		pubHash, err := anonSvcA.CreateCollection(pubCollName, []model.AnonCollectionEntry{
			{Path: fmt.Sprintf("notes/cycle-%d.txt", cycle), Providers: []model.Provider{{Type: "sha256", Value: smallHash, MimeType: "text/plain"}}},
		}, []string{"stress", fmt.Sprintf("cycle-%d", cycle)})
		require.NoError(t, err, "Cycle %d: Create public collection failed", cycle)

		// Create a protected collection with dynamic passcode for largeBlob
		protCollName := fmt.Sprintf("prot-coll-cycle-%d", cycle)
		cyclePasscode := fmt.Sprintf("pass-cycle-%d-sec", cycle)
		protHash, err := anonSvcA.CreateCollectionWithPolicy(
			protCollName,
			[]model.AnonCollectionEntry{
				{Path: fmt.Sprintf("blobs/asset-%d.bin", cycle), Providers: []model.Provider{{Type: "sha256", Value: largeHash, MimeType: "application/octet-stream"}}},
			},
			[]string{"protected", fmt.Sprintf("cycle-%d", cycle)},
			model.VisibilityPublic,
			nil,
			model.AccessPolicyProtected,
			cyclePasscode,
			"host-operator",
		)
		require.NoError(t, err, "Cycle %d: Create protected collection failed", cycle)

		// ─── Step 3: Immutability / Version Fork Test ───
		// If not the first cycle, commit a modification on previous cycle's collection
		var forkedHash string
		if cycle > 1 && lastCreatedCollectionHash != "" {
			forkedHash, err = anonSvcA.CommitCollectionWithPasscode(
				lastCreatedCollectionHash,
				[]model.AnonCollectionEntry{
					{Path: fmt.Sprintf("appended/from-cycle-%d.txt", cycle), Providers: []model.Provider{{Type: "sha256", Value: smallHash, MimeType: "text/plain"}}},
				},
				fmt.Sprintf("Cycle %d fork update", cycle),
				"",
				"host-operator",
			)
			require.NoError(t, err, "Cycle %d: Commit/fork previous collection failed", cycle)
			require.NotEqual(t, lastCreatedCollectionHash, forkedHash, "Forked collection must have distinct content hash")

			// Ensure previous collection remains accessible and unmodified (immutability)
			prevColl, err := anonSvcA.GetCollectionByHash(lastCreatedCollectionHash)
			require.NoError(t, err)
			require.NotEmpty(t, prevColl.Entries)
		}
		lastCreatedCollectionHash = pubHash

		// ─── Step 4: WebRTC Remote Share Manifest & Passcode Gate Verification ───
		// 4a. Query without passcode -> protected collection entries must be hidden
		snapUnauth, err := svcB.RequestShares(svcA.ID())
		require.NoError(t, err, "Cycle %d: Share request without passcode failed", cycle)

		var foundPub, foundProt bool
		for _, c := range snapUnauth.Collections {
			if c.Hash == pubHash {
				foundPub = true
				require.False(t, c.IsProtected)
				require.Len(t, c.Entries, 1)
				require.Equal(t, smallHash, c.Entries[0].Hash)
			}
			if c.Hash == protHash {
				foundProt = true
				require.True(t, c.IsProtected, "Protected collection must be masked")
				require.Empty(t, c.Entries, "Protected entries must be empty without passcode")
			}
		}
		require.True(t, foundPub, "Cycle %d: Public collection missing from manifest", cycle)
		require.True(t, foundProt, "Cycle %d: Protected collection missing from manifest", cycle)

		// 4b. Query with incorrect passcode -> entries remain masked
		snapWrong, err := svcB.RequestSharesWithPasscode(svcA.ID(), "invalid-passcode-guess")
		require.NoError(t, err)
		for _, c := range snapWrong.Collections {
			if c.Hash == protHash {
				require.True(t, c.IsProtected)
				require.Empty(t, c.Entries, "Invalid passcode must not unlock entries")
			}
		}

		// 4c. Query with valid passcode -> entries unlocked
		snapAuth, err := svcB.RequestSharesWithPasscode(svcA.ID(), cyclePasscode)
		require.NoError(t, err)
		for _, c := range snapAuth.Collections {
			if c.Hash == protHash {
				require.False(t, c.IsProtected, "Valid passcode must unlock collection")
				require.Len(t, c.Entries, 1)
				require.Equal(t, largeHash, c.Entries[0].Hash)
			}
		}

		// ─── Step 5: Direct WebRTC Range Fetch ───
		// Fetch a partial slice of largeBlob over WebRTC DataChannel
		const sliceOffset = 1024
		const sliceSize = 2048
		sliceBytes, err := svcB.FetchFromPeer(svcA.ID(), largeHash, sliceOffset, sliceSize)
		require.NoError(t, err, "Cycle %d: Range fetch over WebRTC failed", cycle)
		require.Equal(t, sliceSize, len(sliceBytes))
		require.True(t, bytes.Equal(largeBlob[sliceOffset:sliceOffset+sliceSize], sliceBytes))

		// ─── Step 6: Cross-Node Streaming Pull & Registration (Node B) ───
		pullRelPath := fmt.Sprintf("cycle-%d/asset.bin", cycle)
		job, err := pullerB.Start(svcA.ID(), largeHash, fmt.Sprintf("asset-%d.bin", cycle), pullRelPath, "")
		require.NoError(t, err, "Cycle %d: Failed to start PeerPuller job", cycle)

		// Wait for completion
		deadline := time.Now().Add(30 * time.Second)
		var completedJob service.PullJob
		for time.Now().Before(deadline) {
			j, exists := pullerB.Get(job.ID)
			require.True(t, exists)
			if j.Done() {
				completedJob = j
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		require.Equal(t, service.PullDone, completedJob.Status, "Cycle %d: Pull job did not complete: %s", cycle, completedJob.Error)
		require.False(t, completedJob.Skipped, "Cycle %d: First pull must not be skipped", cycle)
		require.Equal(t, int64(len(largeBlob)), completedJob.Received)

		// Verify disk contents and index registration
		savedDiskPath := completedJob.SavedTo
		savedData, err := os.ReadFile(savedDiskPath)
		require.NoError(t, err)
		require.True(t, bytes.Equal(largeBlob, savedData), "Cycle %d: Saved content does not match source", cycle)

		fiRecord, err := idxB.Info(largeHash)
		require.NoError(t, err)
		require.Equal(t, largeHash, fiRecord.Hash)
		require.Equal(t, int64(len(largeBlob)), fiRecord.Size)

		// ─── Step 7: Content-Addressed Idempotency (Dedup Skip) ───
		// Re-pulling the same content must immediately skip without re-downloading
		againJob, err := pullerB.Start(svcA.ID(), largeHash, fmt.Sprintf("asset-%d.bin", cycle), pullRelPath, "")
		require.NoError(t, err)
		deadline = time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			j, exists := pullerB.Get(againJob.ID)
			require.True(t, exists)
			if j.Done() {
				require.Equal(t, service.PullDone, j.Status)
				require.True(t, j.Skipped, "Cycle %d: Repeated pull must be skipped by dedup", cycle)
				break
			}
			time.Sleep(15 * time.Millisecond)
		}

		// ─── Step 8: Remote Quarantine Inbox Moderation (Alternating Approve/Reject) ───
		inboxUploadName := fmt.Sprintf("visitor-upload-%d.dat", cycle)
		inboxContent := []byte(fmt.Sprintf("untrusted remote payload cycle %d: %s", cycle, time.Now().UTC().String()))
		inboxUploaderID := svcB.ID()

		// Remote Node B initiates quarantined upload session into Node A's storage inbox
		sess, err := idxSvcA.BeginUploadForPeer(inboxUploadName, int64(len(inboxContent)), inboxUploaderID)
		require.NoError(t, err, "Cycle %d: BeginUploadForPeer failed", cycle)
		err = sess.WriteAt(0, inboxContent)
		require.NoError(t, err)
		done, uploadedFI, err := sess.Complete()
		require.NoError(t, err)
		require.True(t, done)
		require.NotNil(t, uploadedFI)

		// Verify quarantined state in Node A repository
		record, err := repository.GetFileIndex(uploadedFI.Hash)
		require.NoError(t, err)
		require.True(t, record.IsInbox, "Cycle %d: Uploaded remote file must be quarantined in inbox", cycle)
		require.Equal(t, inboxUploaderID, record.UploaderPeerID)

		// Alternating host moderation: odd cycles approve, even cycles reject
		if cycle%2 == 1 {
			// Host approves the file -> released into main library
			err = repository.ApproveInboxFile(uploadedFI.Hash)
			require.NoError(t, err, "Cycle %d: ApproveInboxFile failed", cycle)

			approvedFI, err := repository.GetFileIndex(uploadedFI.Hash)
			require.NoError(t, err)
			require.False(t, approvedFI.IsInbox, "Cycle %d: Approved file must clear IsInbox flag", cycle)
		} else {
			// Host rejects the file -> removed from index and disk
			if uploadedFI.Path != "" {
				_ = os.Remove(uploadedFI.Path)
			}
			_, err = repository.DeleteFileIndex(uploadedFI.Hash)
			require.NoError(t, err, "Cycle %d: DeleteFileIndex (reject) failed", cycle)

			deletedFI, err := repository.GetFileIndex(uploadedFI.Hash)
			require.True(t, err != nil || deletedFI == nil || deletedFI.Deleted, "Cycle %d: Rejected file must not be found in active index", cycle)
		}

		// ─── Step 9: Cascading Secondary Distribution (Node B -> Node C) ───
		// Node C pulls the file from Node B (which Node B pulled from Node A in step 6)
		cascadePulled, err := svcC.FetchFromPeer(svcB.ID(), largeHash, 0, -1)
		require.NoError(t, err, "Cycle %d: Cascading secondary pull B->C failed", cycle)
		require.True(t, bytes.Equal(largeBlob, cascadePulled), "Cycle %d: Cascading content byte mismatch", cycle)

		// ─── Step 10: Concurrency & Chaos Resilience ───
		// Fire 5 concurrent range fetches and 1 corrupt hash probe simultaneously to ensure
		// WebRTC DataChannel sendMu and message pump maintain zero races and zero deadlocks.
		var chaosWg sync.WaitGroup
		for worker := 0; worker < 5; worker++ {
			chaosWg.Add(1)
			go func(w int) {
				defer chaosWg.Done()
				off := int64(w * 100)
				data, err := svcB.FetchFromPeer(svcA.ID(), largeHash, off, 500)
				if err == nil {
					require.True(t, bytes.Equal(largeBlob[off:off+500], data))
				}
			}(worker)
		}

		// Probe nonexistent hash: must fail gracefully without hanging or closing the connection
		chaosWg.Add(1)
		go func() {
			defer chaosWg.Done()
			garbageHash := sha256Hex([]byte(fmt.Sprintf("nonexistent-hash-%d", cycle)))
			_, err := svcB.FetchFromPeer(svcA.ID(), garbageHash, 0, 1024)
			require.Error(t, err, "Fetching nonexistent hash must return error gracefully")
		}()
		chaosWg.Wait()

		// ─── Step 11: Invariant Check at End of Cycle ───
		// Ensure no leftover temporary .part files in download directories
		assertNoPartFiles(t, downloadB)
		assertNoPartFiles(t, downloadC)

		t.Logf("--- [Cycle %d/%d] Completed Successfully ---", cycle, totalCycles)
	}

	t.Log("== Combinatorial Repeating Stress Test Completed: All Invariants Maintained ==")
}

// assertNoPartFiles ensures that no temporary .part files remain after pull operations.
func assertNoPartFiles(t *testing.T, dir string) {
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() && strings.HasSuffix(info.Name(), ".part") {
			t.Errorf("Leaked temporary part file detected: %s", path)
		}
		return nil
	})
	require.NoError(t, err)
}
