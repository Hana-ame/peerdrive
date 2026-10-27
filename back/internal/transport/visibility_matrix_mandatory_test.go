//go:build mandatory

// visibility_matrix_mandatory_test.go
//
// 🚨 MANDATORY DIRECTIVE FOR ALL AI AGENTS:
// This test group is part of the mandatory (human-verified) test category.
// AI agents are STRICTLY FORBIDDEN from modifying, commenting out, or loosening
// these assertions. If any of them fails, fix the implementation to satisfy the
// invariant — never weaken the assertion. To change an invariant, a human must
// update test/mandatory/manifest.sha256 in the same review.
//
// 这些测试钉住 peerdrive 的权限语义：**公 / 不列出 / 私有** 三档在
// 「能不能列出来」和「能不能取到」上到底怎么分工。
//
// 为什么必须端到端、必须真实装配：三档语义分散在两层里——
//   - transport 层：pskGate（准入）→ anonGate → 能力检查 → serveFile 的
//     AuthorizeDownload / serveShare
//   - service 层：NodeShare 把运营者的共享声明解析成 ShareSnapshot，
//     filesSnapshotFor 做「列不列」，AllowsDownload 做「给不给」
//
// 单层测试（纯 service 或纯 transport）都会漏掉「装配」这一环：builder.go 里
// SetShareGate / SetShareProvider / SetShareProviderWithToken 三条线只要有一条
// 没接上，权限体系整体失效，但两层各自的单测全绿。所以这里复刻 builder.go 的
// 生产装配（见 buildVisibilityHarness），并全程走真实入站 dispatchFrame。
//
// 与需求描述的映射（重要，别当成实现 bug）：
//   - 需求里的「list」在本代码库是**管理动词**，返回 file_index 全量
//     （inbound.go serveList，只做路径脱敏、不做权限过滤）。**带权限过滤的
//     目录清单是 share 动词**——serveShare 注释原文：
//     "unlisted is never listed, public is always listed"。
//     所以下面用 share 验证「能看到 public，看不到其他」。
//   - 需求里的「404 / 200」是 HTTP 语义。P2P 帧上没有 HTTP 状态码：
//     「拒」= 一个 err 帧，「给」= meta + data + done。断言按这个对应写。
//   - 需求里的「get private with PSK -> 200」在**文件**层面不成立：PSK 是
//     节点级准入门禁，文件级 private 靠好友名单（NodeShare.AllowsDownload →
//     isFriend）。PSK 解锁私有内容的真实机制在**合集**层面
//     （AccessPolicy=psk + VerifyPSK(token)，Visibility 仍为 public）。
//     两条都被钉住；其中 TestMandatory_PeerJSPSKGateAndCollectionVisibility 里
//     「PSK 已认证但非好友 → 仍拒」是**故意的安全断言**——如果把 PSK 改成
//     能解锁 private 文件，这条会变红。那是共享密钥级别的降权，必须人来拍板。
//
// 跑法：
//   go test -tags "nosqlite mandatory" ./internal/transport/ \
//       -run TestMandatory_PeerJS -v
//
// 不用 t.Parallel() 是故意的：repository.db 是包级全局，initTestDB 会替换它，
// 两个并行测试会互相覆盖对方的 DB 句柄；本文件的「不存在的文件 → 拒」
// 断言对跨测试串数据特别敏感。串行代价约 0.05s，值得。

package transport

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"peerdrive/internal/config"
	"peerdrive/internal/model"
	"peerdrive/internal/service"
)

const (
	mandatoryFilePublic   = "PUB-file-content-0123456789"
	mandatoryFileUnlisted = "UNL-file-content-0123456789"
	mandatoryFilePrivate  = "PRV-file-content-0123456789"
	mandatoryNotExistHash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	mandatoryNodePSK      = "mandatory-node-psk"
	mandatoryCollPSK      = "mandatory-coll-psk"
	mandatoryPeerID       = "stranger-peer"
	mandatoryFriendID     = "buddy-peer"
)

// visibilityIDs harness 造出来的全部内容标识。断言只引用它，不自己猜。
type visibilityIDs struct {
	pubFile, unlFile, prvFile string
	pubColl, prvColl, pskColl string
}

// buildVisibilityHarness 复刻 builder.go 的生产装配：真实 NodeShare + 三档文件
// 声明 + 三个合集声明 + 好友名单 +（可选）PSK。
//
// 三条接线一条都不能少（任缺一条，权限体系整体失效而单层单测全绿）：
//   - SetShareGate              → 下载门禁（req 动词的 private 判定）
//   - SetShareProvider          → 不带 token 的清单（share 动词）
//   - SetShareProviderWithToken → 带 token 的清单（合集 PSK / passcode）
func buildVisibilityHarness(t *testing.T, psk string) (*PeerJSService, *service.NodeShare, visibilityIDs) {
	t.Helper()

	cfg := &config.Config{}
	if psk != "" {
		cfg.PeerPSK = psk
	}
	// file_index 走 repository 的全局 DB，必须先初始化。
	initTestDB(t)

	idx := NewFileIndexService(t.TempDir())
	svc := newTestPeerJSServiceWithIndex(t, idx)
	svc.cfg = cfg // 默认构造给的是空 Config，这里换成带 PSK 的

	share := service.NewNodeShare(cfg, "") // 空 storageDir = 内存模式，不落盘

	// 三个真实文件 + 三档声明
	hp := seedMandatoryFile(t, svc, "mandatory-public.bin", []byte(mandatoryFilePublic))
	hu := seedMandatoryFile(t, svc, "mandatory-unlisted.bin", []byte(mandatoryFileUnlisted))
	hv := seedMandatoryFile(t, svc, "mandatory-private.bin", []byte(mandatoryFilePrivate))

	// 三个合集，各带一条真实条目。合集的「ID」就是它的内容地址，
	// 这里用确定性的 64-hex 代替 AnonService 的落盘实现——
	// SetAnonAccess 收的是函数指针，不关心数据从哪来，这样测试不碰
	// repository 的包级全局。
	entry := func(path, content string) []model.AnonCollectionEntry {
		return []model.AnonCollectionEntry{{
			Path:      path,
			Providers: []model.Provider{{Type: "sha256", Value: sha256hex([]byte(content)), MimeType: "text/plain"}},
		}}
	}
	pubColl := sha256hex([]byte("mandatory-public-collection"))
	prvColl := sha256hex([]byte("mandatory-private-collection"))
	pskColl := sha256hex([]byte("mandatory-psk-collection"))
	store := map[string]*model.AnonCollection{
		pubColl: {FriendlyName: "public collection", Entries: entry("docs/public.txt", "public-coll-entry")},
		prvColl: {FriendlyName: "private collection", Visibility: model.VisibilityPrivate, Entries: entry("docs/private.txt", "private-coll-entry")},
		// PSK 合集 = 壳子对所有人可见 + 条目锁在 PSK 后面。
		// 用 VisibilityPublic 而不是 VisibilityPSK 是有意的：nodeshare_snapshot.go:284
		// 只要 EffectiveVisibility() != public 就降级为 private，而 :287 的
		// `requester == "" || !CanView(...)` 在 tokenVerifier 未接线（builder.go
		// 生产装配不调 SetTokenVerifier）时恒真——所以 VisibilityPSK 的合集
		// 对陌生人**永远**被过滤掉，PSK 解锁路径根本走不到。真正的合集级 PSK
		// 机制是 AccessPolicy=psk + PSKHash（Visibility 保持 public）。
		pskColl: {FriendlyName: "psk collection", Visibility: model.VisibilityPublic, AccessPolicy: model.AccessPolicyPSK, PSKHash: sha256hex([]byte(mandatoryCollPSK)), Entries: entry("docs/psk.txt", "psk-coll-entry")},
	}
	share.SetAnonAccess(
		func(hash string) (*model.AnonCollection, error) {
			c, ok := store[hash]
			if !ok {
				return nil, fmt.Errorf("collection %s not found", hash)
			}
			return c, nil
		},
		func() ([]model.AnonCollectionSummary, error) { return []model.AnonCollectionSummary{}, nil },
	)
	share.SetFileLister(func() ([]FileInfo, error) { return svc.FileIndex().List(0, 1000) })
	share.SetFileInfoReader(svc.FileIndex().Info)
	svc.SetShareProvider(share.SnapshotFor)
	svc.SetShareProviderWithToken(share.SnapshotForToken)
	svc.SetShareGate(share)

	_, err := share.Update(service.ScopePatch{
		Enable: boolPtr(true),
		Files: itemPtr([]service.ShareItem{
			{ID: hp, Level: "public"},
			{ID: hu, Level: "unlisted"},
			{ID: hv, Level: "private"},
		}),
		Friends: strPtr([]string{mandatoryFriendID}),
		Collections: itemPtr([]service.ShareItem{
			{ID: pubColl, Level: "public"},
			{ID: prvColl, Level: "private"},
			// psk 合集的 scope 级别必须是 public——否则 :291 把它按 private 过滤掉，
			// 条目锁（isProtected）这条分支永远走不到。
			{ID: pskColl, Level: "public"},
		}),
	})
	require.NoError(t, err, "声明三档文件 + 三个合集 + 好友名单必须成功")

	return svc, share, visibilityIDs{
		pubFile: hp, unlFile: hu, prvFile: hv,
		pubColl: pubColl, prvColl: prvColl, pskColl: pskColl,
	}
}

// ---- 会话与帧的薄封装 ----

// connectPeer 建立一条对端连接（真实 bindConn + connState）。
func connectPeer(t *testing.T, svc *PeerJSService, peerID string) *fakeSession {
	t.Helper()
	sess := &fakeSession{id: peerID}
	svc.bindConn(sess)
	require.NotNil(t, svc.pending[sess], "bindConn must register a connState")
	return sess
}

// frame 发任意一帧。
func frame(t *testing.T, svc *PeerJSService, sess *fakeSession, payload map[string]any) {
	t.Helper()
	svc.dispatchFrame(sess, svc.pending[sess], pskFrame(t, payload))
}

// pskAuth 在会话上先过 PSK 准入。PSK 模式下每个新连接都要先做这一步，
// 否则后续任何 servedVerb（req / share / list …）都会被 pskGate 挡下。
//
// 每个 case 用**新会话**重新认证：同一个 peerID 的多个 connState 会被
// dedup 关掉（conn.go「dedup connection to <peer>, closing stale」），
// 复用已认证的会话反而会把它关掉，断言就变成超时。
func pskAuth(t *testing.T, svc *PeerJSService, peerID, psk, reqID string) *fakeSession {
	t.Helper()
	sess := connectPeer(t, svc, peerID)
	frame(t, svc, sess, map[string]any{"type": "psk-auth", "psk": psk, "reqId": reqID})
	return sess
}

// reqAndWait 在已有会话上发 req 并等终态帧。
func reqAndWait(t *testing.T, svc *PeerJSService, sess *fakeSession, hash, reqID string) []mfr {
	t.Helper()
	frame(t, svc, sess, map[string]any{
		"type":   "req",
		"hash":   hash,
		"offset": int64(0),
		"size":   int64(-1),
		"reqId":  reqID,
	})
	return mfrPoll(sess, reqID, 10*time.Second)
}

// shareAndWait 在已有会话上发 share 并返回 ShareSnapshot。
//
// token 是合集级凭据，走 dcResp.Token（share.go serveShare 依次读
// r.Token / r.Passcode / r.Passkey）——**不是** r.Psk，后者是节点级 PSK。
// 写错字段的话合集会安静地保持锁定，测试变成假通过。
func shareAndWait(t *testing.T, svc *PeerJSService, sess *fakeSession, reqID, token string) *model.ShareSnapshot {
	t.Helper()
	payload := map[string]any{"type": "share", "reqId": reqID}
	if token != "" {
		payload["token"] = token
	}
	frame(t, svc, sess, payload)
	// serveShare 是 goroutine（conn.go:601 `go s.serveShare(c, r)`），要轮询等。
	deadline := time.Now().Add(5 * time.Second)
	snap := shareSnapshotFor(sess, reqID)
	for snap == nil && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
		snap = shareSnapshotFor(sess, reqID)
	}
	require.NotNil(t, snap, "share 动词必须返回 share-resp 帧")
	return snap
}

func boolPtr(v bool) *bool { return &v }

func strPtr(v []string) *[]string { return &v }

func itemPtr(v []service.ShareItem) *[]service.ShareItem { return &v }

// mfrStrict 只收集 reqId 精确匹配的帧。
//
// 为什么不复用 collectMFR：它会把「没有 reqId 的帧」也带进来（bindConn 时的
// cap 公告、psk-ok 回执）。断言「被拒」时必须严格——半吐一帧 data 再发 err
// 同样是泄露，多进来的公告帧会掩盖这个漏洞。
// serveFile / pskGate / serveShare 的每个响应都回显 ReqID（inbound.go、
// gate.go pskGate、share.go），所以精确过滤不会漏掉该算的帧。
func mfrStrict(s *fakeSession, reqID string) []mfr {
	out := []mfr{}
	for _, fr := range s.sentFrames() {
		rid, _ := fr.header["reqId"].(string)
		if rid != reqID {
			continue
		}
		f := mfr{reqID: reqID}
		if v, ok := fr.header["type"].(string); ok {
			f.typ = v
		}
		if v, ok := fr.header["hash"].(string); ok {
			f.hash = v
		}
		if v, ok := fr.header["total"].(float64); ok {
			f.total = int64(v)
		}
		if v, ok := fr.header["offset"].(float64); ok {
			f.offset = int64(v)
		}
		if v, ok := fr.header["size"].(float64); ok {
			f.size = int64(v)
		}
		if v, ok := fr.header["msg"].(string); ok {
			f.msg = v
		}
		f.body = fr.body
		out = append(out, f)
	}
	return out
}

// mfrPoll 轮询某个 reqId 的帧，直到出现终态帧（done / err）或超时。
func mfrPoll(s *fakeSession, reqID string, d time.Duration) []mfr {
	deadline := time.Now().Add(d)
	for {
		frames := mfrStrict(s, reqID)
		for _, f := range frames {
			if f.typ == "done" || f.typ == "err" {
				return frames
			}
		}
		if time.Now().After(deadline) {
			return frames
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// assertRefused 断言收到「拒」：恰好一个 err 帧，且没有任何 meta/data/done。
// 只收到 err 还不够——半吐（先吐了 data 又发 err）同样是漏洞，
// 对端拼出内容就是泄露。
func assertRefused(t *testing.T, label, hash string, frames []mfr) {
	t.Helper()
	var errs []mfr
	for _, f := range frames {
		if f.typ == "err" {
			errs = append(errs, f)
		}
	}
	require.NotEmpty(t, errs, "%s: req %s 必须返回 err 帧（不能静默丢弃，调用方无法自查）", label, hash)
	require.Len(t, errs, 1, "%s: req %s 只应有一个 err 帧，实际 %d", label, hash, len(errs))
	require.NotEmpty(t, errs[0].msg, "%s: err 帧必须带 msg，否则调用方无法自查（frames=%v）", label, mfrTypes(frames))
	for _, f := range frames {
		if f.typ != "err" {
			assert.Failf(t, "%s: req %s 被拒后不应出现 %s 帧（半吐 = 泄露）", label, hash, f.typ)
		}
	}
}

// assertServed 断言收到「给」：恰好一个 meta、一个 done，data 重组等于内容。
// frames 是调用方已经收好的一批帧——断言函数本身不产生帧，避免「先断言后发帧」
// 这种顺序错误导致的假阴性。
func assertServed(t *testing.T, label, hash string, frames []mfr, want []byte) {
	t.Helper()

	var metas, dones, datas []mfr
	for _, f := range frames {
		switch f.typ {
		case "meta":
			metas = append(metas, f)
		case "done":
			dones = append(dones, f)
		case "data":
			datas = append(datas, f)
		}
	}

	for _, f := range frames {
		if f.typ == "err" {
			require.Fail(t, fmt.Sprintf("%s: req %s 应当成功却返回 err: msg=%q frames=%v", label, hash, f.msg, mfrTypes(frames)))
		}
		if f.hash != "" {
			assert.Equal(t, hash, f.hash, "%s: %s 帧必须回显请求的 hash", label, f.typ)
		}
	}

	require.Len(t, metas, 1, "%s: 必须收到恰好一个 meta 帧", label)
	require.Len(t, dones, 1, "%s: 必须收到恰好一个 done 帧", label)

	var got []byte
	for _, f := range datas {
		got = append(got, f.body...)
	}
	assert.Equal(t, want, got, "%s: 重组出的字节必须等于真实文件内容", label)
	assert.Equal(t, sha256hex(want), sha256hex(got), "%s: 对端拿到的内容必须哈希为它请求的那个 hash", label)
	assert.Equal(t, int64(len(want)), dones[0].size, "%s: done.size 必须等于实际发送的字节数", label)
}

// TestMandatory_PeerJSVisibilityListing 陌生人经 P2P 问「你共享了什么」：
// 清单里只能有 public，看不到 unlisted、private，也看不到不存在的文件。
func TestMandatory_PeerJSVisibilityListing(t *testing.T) {
	svc, _, ids := buildVisibilityHarness(t, "")

	snap := shareAndWait(t, svc, connectPeer(t, svc, mandatoryPeerID), "sh1", "")

	fileHashes := map[string]string{}
	for _, f := range snap.Files {
		fileHashes[f.Hash] = f.Name
	}
	assert.Contains(t, fileHashes, ids.pubFile, "public 文件必须出现在清单里")
	assert.NotContains(t, fileHashes, ids.unlFile, "unlisted 文件绝不能出现在清单里（不列出就是不列出）")
	assert.NotContains(t, fileHashes, ids.prvFile, "private 文件绝不能出现在清单里（陌生人）")
	assert.NotContains(t, fileHashes, mandatoryNotExistHash, "不存在的文件绝不能出现在清单里")
	assert.Len(t, fileHashes, 1, "陌生人的清单应当只有 1 个文件，实际 %v", fileHashes)

	collSet := map[string]bool{}
	for _, c := range snap.Collections {
		collSet[c.Hash] = true
	}
	assert.True(t, collSet[ids.pubColl], "public 合集必须出现在清单里")
	assert.False(t, collSet[ids.prvColl], "private 合集绝不能出现在清单里（陌生人）")
	assert.True(t, collSet[ids.pskColl],
		"PSK 合集的壳子应当出现在清单里（元信息对所有人可见，条目才锁在 PSK 后面）")
}

// TestMandatory_PeerJSVisibilityListingForFriend 同一个节点、同一个 share 动词，
// 换个身份（好友）清单就应当包含 private —— 权限是**按请求者**算的，
// 不是节点级的开关。
func TestMandatory_PeerJSVisibilityListingForFriend(t *testing.T) {
	svc, _, ids := buildVisibilityHarness(t, "")

	snap := shareAndWait(t, svc, connectPeer(t, svc, mandatoryFriendID), "sh2", "")

	fileHashes := map[string]bool{}
	for _, f := range snap.Files {
		fileHashes[f.Hash] = true
	}
	assert.True(t, fileHashes[ids.pubFile], "好友也能看到 public")
	assert.True(t, fileHashes[ids.prvFile], "好友必须能看到 private（否则 private 对好友毫无意义）")
	assert.False(t, fileHashes[ids.unlFile], "unlisted 对任何人都绝不列出——好友也不行")
	assert.Len(t, fileHashes, 2, "好友的清单应当有 2 个文件，实际 %v", fileHashes)

	collSet := map[string]bool{}
	for _, c := range snap.Collections {
		collSet[c.Hash] = true
	}
	assert.True(t, collSet[ids.prvColl], "private 合集对好友应当可见")
}

// TestMandatory_PeerJSDownloadByTier 取内容：public / unlisted 陌生人可取，
// private / 不存在的 hash 被拒。
func TestMandatory_PeerJSDownloadByTier(t *testing.T) {
	svc, _, ids := buildVisibilityHarness(t, "")

	t.Run("public 陌生人可取", func(t *testing.T) {
		sess := connectPeer(t, svc, mandatoryPeerID)
		assertServed(t, "public", ids.pubFile, reqAndWait(t, svc, sess, ids.pubFile, "r-pub"), []byte(mandatoryFilePublic))
	})
	t.Run("unlisted 陌生人可取（不列出但可取）", func(t *testing.T) {
		sess := connectPeer(t, svc, mandatoryPeerID)
		assertServed(t, "unlisted", ids.unlFile, reqAndWait(t, svc, sess, ids.unlFile, "r-unl"), []byte(mandatoryFileUnlisted))
	})
	t.Run("private 陌生人被拒", func(t *testing.T) {
		sess := connectPeer(t, svc, mandatoryPeerID)
		assertRefused(t, "陌生人", ids.prvFile, reqAndWait(t, svc, sess, ids.prvFile, "r-prv"))
	})
	t.Run("不存在的 hash 被拒", func(t *testing.T) {
		sess := connectPeer(t, svc, mandatoryPeerID)
		assertRefused(t, "不存在的 hash", mandatoryNotExistHash, reqAndWait(t, svc, sess, mandatoryNotExistHash, "r-nope"))
	})
}

// TestMandatory_PeerJSPSKGateAndCollectionVisibility 两件事：
//  1. PSK 是**准入**门禁，不是文件级私钥。
//  2. 合集层面的 PSK 才是「凭据解锁私有内容」的真实机制。
func TestMandatory_PeerJSPSKGateAndCollectionVisibility(t *testing.T) {
	svc, _, ids := buildVisibilityHarness(t, mandatoryNodePSK)

	// --- 1a. 未认证的陌生人：连 public 都取不到（PSK 是整个节点的准入） ---
	anon := connectPeer(t, svc, mandatoryPeerID)
	assertRefused(t, "未认证", ids.pubFile, reqAndWait(t, svc, anon, ids.pubFile, "g1"))

	// --- 1b. PSK 认证过但非好友：private 仍然取不到 ---
	// 这是**故意**的安全断言：PSK 是共享密钥，把「过了准入」当「有下载权」
	// 等于把 private 悄悄降成 unlisted。要解锁 private 文件必须走好友名单。
	// 如果哪天有人为了让「get private with PSK -> 200」成立而改实现，
	// 这条会变红——那是共享密钥级别的降权，必须人来拍板。
	authed := pskAuth(t, svc, mandatoryPeerID, mandatoryNodePSK, "h1")
	assertRefused(t, "PSK 已认证但非好友", ids.prvFile, reqAndWait(t, svc, authed, ids.prvFile, "g2"))

	// --- 1c. 好友（同样已认证）：private 可取 ---
	friend := pskAuth(t, svc, mandatoryFriendID, mandatoryNodePSK, "h2")
	assertServed(t, "好友", ids.prvFile, reqAndWait(t, svc, friend, ids.prvFile, "g3"), []byte(mandatoryFilePrivate))

	// --- 2a. 合集 PSK：没带 token → 出现但锁定，条目为空 ---
	snap := shareAndWait(t, svc, pskAuth(t, svc, mandatoryPeerID, mandatoryNodePSK, "h3"), "c1", "")
	locked := findColl(t, snap, ids.pskColl)
	require.NotNil(t, locked, "PSK 合集未带 token 时应以锁定形态出现（元信息可见）")
	assert.True(t, locked.IsProtected, "未带 token 的 PSK 合集必须标记 IsProtected")
	assert.Empty(t, locked.Entries, "未带 token 时绝不能泄漏条目（hash/path/mime）")

	// --- 2b. 带正确 token → 解锁，条目可见 ---
	snap = shareAndWait(t, svc, pskAuth(t, svc, mandatoryPeerID, mandatoryNodePSK, "h4"), "c2", mandatoryCollPSK)
	unlocked := findColl(t, snap, ids.pskColl)
	require.NotNil(t, unlocked, "带 token 时 PSK 合集仍应在清单里")
	assert.False(t, unlocked.IsProtected, "带正确 PSK 后必须解锁")
	require.NotEmpty(t, unlocked.Entries, "带正确 PSK 后必须给出条目")
	assert.Equal(t, "docs/psk.txt", unlocked.Entries[0].Path, "条目路径必须是合集里的真实条目")

	// --- 2c. 带错误 token → 仍然锁定 ---
	snap = shareAndWait(t, svc, pskAuth(t, svc, mandatoryPeerID, mandatoryNodePSK, "h5"), "c3", "wrong-key")
	still := findColl(t, snap, ids.pskColl)
	require.NotNil(t, still)
	assert.True(t, still.IsProtected, "错误 PSK 必须仍然锁定")
	assert.Empty(t, still.Entries, "错误 PSK 绝不能泄漏条目")

	// --- 2d. private 合集对好友可见 ---
	snapFriend := shareAndWait(t, svc, pskAuth(t, svc, mandatoryFriendID, mandatoryNodePSK, "h6"), "c4", "")
	require.NotNil(t, findColl(t, snapFriend, ids.prvColl), "private 合集对好友应当可见")
}

// shareSnapshotFor 从会话里取出某个 reqId 的 share-resp，解成 ShareSnapshot。
//
// 为什么不复用 mfr（header map）：share-resp 嵌 ShareSnapshot，字段是
// []ShareCollectionInfo / []ShareFileInfo。fakeSession 把 header 存成
// map[string]any 时，空数组 [] 会退化成 nil，丢掉「空清单」和「空条目」的
// 区别——而那正是断言目标。所以这里直接读原始 JSON 字节。
func shareSnapshotFor(s *fakeSession, reqID string) *model.ShareSnapshot {
	for _, b := range s.sentJSON {
		var fr map[string]any
		if err := json.Unmarshal(b, &fr); err != nil {
			continue
		}
		if fr["type"] != "share-resp" {
			continue
		}
		if rid, _ := fr["reqId"].(string); rid != "" && rid != reqID {
			continue
		}
		var snap model.ShareSnapshot
		if err := json.Unmarshal(b, &snap); err != nil {
			return nil
		}
		return &snap
	}
	return nil
}

func findColl(t *testing.T, snap *model.ShareSnapshot, hash string) *model.ShareCollectionInfo {
	t.Helper()
	for i := range snap.Collections {
		if snap.Collections[i].Hash == hash {
			return &snap.Collections[i]
		}
	}
	return nil
}
