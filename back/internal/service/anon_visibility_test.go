package service

import (
	"os"
	"testing"

	"peerdrive/internal/model"

	"github.com/stretchr/testify/assert"
)

func validEntries() []model.AnonCollectionEntry {
	return []model.AnonCollectionEntry{
		{Path: "README.md", Hash: "a7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a"},
	}
}

// Discovery background: the frontend "broadcast" was changed to three options
// (public/specified permissions/only self), whether a restricted collection can
// be seen by others depends entirely on CanView; and the easiest thing to get
// wrong in CanView is treating "no account (unauthenticated request)" as
// allowed.
func TestAnonCollection_CanView(t *testing.T) {
	coll := &model.AnonCollection{Owner: "alice", Visibility: model.VisibilityRestricted, AccessList: []string{"bob"}}

	assert.True(t, coll.CanView("bob"), "account in access list should be allowed")
	assert.True(t, coll.CanView("alice"), "owner should be allowed even outside the list")
	assert.False(t, coll.CanView("carol"), "account outside list should be denied")
	assert.False(t, coll.CanView(""), "unauthenticated request cannot bypass restricted")

	privateColl := &model.AnonCollection{Owner: "alice", Visibility: model.VisibilityPrivate}
	assert.True(t, privateColl.CanView("alice"))
	assert.False(t, privateColl.CanView("bob"))
	assert.False(t, privateColl.CanView(""))

	// Legacy collections don't have a visibility field (JSON written by old
	// versions) → fallback to public
	legacy := &model.AnonCollection{Owner: "alice"}
	assert.Equal(t, model.VisibilityPublic, legacy.EffectiveVisibility())
	assert.True(t, legacy.CanView(""))
}

// Discovery background: when restricted has no access list, the collection
// becomes "visible to nobody" — a typical UI misuse where the user switched the
// option but forgot to pick people. This must be blocked at the service layer,
// rather than silently generating a hash nobody can open.
func TestCreateCollectionWithVisibility_AccessListRequired(t *testing.T) {
	tmpDir, svc := setupAnonServiceTest(t)
	defer os.RemoveAll(tmpDir)

	_, err := svc.CreateCollectionWithVisibility("c", validEntries(), nil, model.VisibilityRestricted, nil, "alice")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "access_list required")

	_, err = svc.CreateCollectionWithVisibility("c", validEntries(), nil, "bogus", []string{"bob"}, "alice")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid visibility")
}

// Discovery background: collections are content-addressed, permissions written
// into JSON participate in the digest — changing permissions must produce a new
// hash. The previous worry was "can the old hash still be read", the answer is
// yes, and it reflects the old permissions (both coexist). This test locks in
// that semantics.
func TestUpdateCollectionVisibility_ProducesNewHash(t *testing.T) {
	tmpDir, svc := setupAnonServiceTest(t)
	defer os.RemoveAll(tmpDir)

	hash, err := svc.CreateCollectionWithVisibility("c", validEntries(), nil, model.VisibilityPublic, nil, "alice")
	assert.NoError(t, err)

	newHash, err := svc.UpdateCollectionVisibility(hash, model.VisibilityPrivate, nil, "alice")
	assert.NoError(t, err)
	assert.NotEqual(t, hash, newHash, "changing permissions must produce a new hash")

	updated, err := svc.GetCollectionByHash(newHash)
	assert.NoError(t, err)
	assert.Equal(t, model.VisibilityPrivate, updated.EffectiveVisibility())
	assert.False(t, updated.CanView("bob"))

	// Old hash still resolves to old permissions (snapshot semantics, not
	// in-place modification)
	old, err := svc.GetCollectionByHash(hash)
	assert.NoError(t, err)
	assert.Equal(t, model.VisibilityPublic, old.EffectiveVisibility())
}

// Discovery background: a third party who gets the hash should not be able to
// modify someone else's permission level. Content-addressed storage has no ACL
// table, the only ownership basis is the Owner field, which must be explicitly
// compared.
func TestUpdateCollectionVisibility_OwnershipEnforced(t *testing.T) {
	tmpDir, svc := setupAnonServiceTest(t)
	defer os.RemoveAll(tmpDir)

	hash, err := svc.CreateCollectionWithVisibility("c", validEntries(), nil, model.VisibilityPublic, nil, "alice")
	assert.NoError(t, err)

	_, err = svc.UpdateCollectionVisibility(hash, model.VisibilityPrivate, nil, "bob")
	assert.Error(t, err, "non-owner cannot change visibility")

	_, err = svc.UpdateCollectionVisibility(hash, model.VisibilityPrivate, nil, "")
	assert.Error(t, err, "unauthenticated request cannot change visibility")
}

// Discovery background: GetCollectionVisibleTo is the only entry point for
// reading collections by remote (future regserver-authenticated peers),
// unauthorized access must manifest as 404 not 403 — otherwise it's
// equivalent to confirming to the attacker "this hash exists and belongs to
// someone else".
func TestGetCollectionVisibleTo_DeniedLooksLikeNotFound(t *testing.T) {
	tmpDir, svc := setupAnonServiceTest(t)
	defer os.RemoveAll(tmpDir)

	hash, err := svc.CreateCollectionWithVisibility("c", validEntries(), nil, model.VisibilityRestricted, []string{"bob"}, "alice")
	assert.NoError(t, err)

	_, err = svc.GetCollectionVisibleTo(hash, "carol")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "collection not found")

	coll, err := svc.GetCollectionVisibleTo(hash, "bob")
	assert.NoError(t, err)
	assert.Equal(t, "c", coll.FriendlyName)
}

// Discovery background (2026-09-19): commit rolls out a new hash, and new
// collections used to be "bare collections with only version/name/entries/tags"
// — a restricted collection would silently revert to public after a single
// commit (content exposed externally via the new hash). This test locks in
// permission inheritance + unauthorized rejection.
func TestCommitCollection_PreservesVisibility(t *testing.T) {
	tmpDir, svc := setupAnonServiceTest(t)
	defer os.RemoveAll(tmpDir)

	hash, err := svc.CreateCollectionWithVisibility("c", validEntries(), nil,
		model.VisibilityRestricted, []string{"bob"}, "alice")
	assert.NoError(t, err)

	newHash, err := svc.CommitCollection(hash, []model.AnonCollectionEntry{
		{Path: "extra.txt", Hash: "b7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a"},
	}, "add extra", "alice")
	assert.NoError(t, err)
	assert.NotEqual(t, hash, newHash, "commit must produce a new hash")

	committed, err := svc.GetCollectionByHash(newHash)
	assert.NoError(t, err)
	assert.Equal(t, model.VisibilityRestricted, committed.EffectiveVisibility(), "commit must not downgrade restricted to public")
	assert.Equal(t, []string{"bob"}, committed.AccessList, "access list must be inherited")
	assert.Equal(t, "alice", committed.Owner, "owner must be inherited")
	assert.False(t, committed.CanView("carol"), "inherited permissions still deny accounts outside the list")
	assert.True(t, committed.CanView("bob"))
}

// Discovery background (same batch): the commit entry point must not read the
// source collection before validating permissions — otherwise someone with a
// hash could commit someone else's private collection, then use the "new
// collection" to write themselves as owner.
func TestCommitCollection_DeniedForNonViewer(t *testing.T) {
	tmpDir, svc := setupAnonServiceTest(t)
	defer os.RemoveAll(tmpDir)

	hash, err := svc.CreateCollectionWithVisibility("c", validEntries(), nil,
		model.VisibilityPrivate, nil, "alice")
	assert.NoError(t, err)

	_, err = svc.CommitCollection(hash, nil, "", "bob")
	assert.Error(t, err, "non-owner cannot commit private collection")
	assert.Contains(t, err.Error(), "source collection not found")
}

// Discovery background (2026-09-19): the controller comment has always said
// "empty hash in entries = delete entry", but the service layer reused the
// creation-time providers validation and judged empty providers as invalid →
// the deletion path was never reached (removeEmpty was dead code). This test
// locks in the "empty providers/empty hash = delete" semantics, while also
// confirming that "only set Hash, no Providers" is normalized to an addition
// rather than a mistaken deletion.
func TestCommitCollection_EmptyHashRemovesEntry(t *testing.T) {
	tmpDir, svc := setupAnonServiceTest(t)
	defer os.RemoveAll(tmpDir)

	hash, err := svc.CreateCollectionWithVisibility("c", validEntries(), nil,
		model.VisibilityPublic, nil, "alice")
	assert.NoError(t, err)

	// Only set Hash (old-format callers) → normalized to addition, must not
	// become deletion
	addHash, err := svc.CommitCollection(hash, []model.AnonCollectionEntry{
		{Path: "second.txt", Hash: "b7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a"},
	}, "add", "alice")
	assert.NoError(t, err)
	added, err := svc.GetCollectionByHash(addHash)
	assert.NoError(t, err)
	assert.Len(t, added.Entries, 2, "legacy-format hash field should be normalized to provider and added")

	// Empty hash / empty providers → delete that entry
	delHash, err := svc.CommitCollection(addHash, []model.AnonCollectionEntry{
		{Path: "second.txt"},
	}, "remove", "alice")
	assert.NoError(t, err)
	deleted, err := svc.GetCollectionByHash(delHash)
	assert.NoError(t, err)
	assert.Len(t, deleted.Entries, 1, "empty hash should delete the corresponding entry")
	assert.Equal(t, "README.md", deleted.Entries[0].Path)
}

// TestCommitCollection_PreservesPasscodeAndAccessPolicy 发现背景：commit 生成新版本时，
// 历史实现遗漏了 AccessPolicy 与 Passcode 的继承，导致受密码保护的合集升级后被降级为无保护公开合集。
func TestCommitCollection_PreservesPasscodeAndAccessPolicy(t *testing.T) {
	tmpDir, svc := setupAnonServiceTest(t)
	defer os.RemoveAll(tmpDir)

	hash, err := svc.CreateCollectionWithPolicy("protected-coll", validEntries(), nil,
		model.VisibilityPublic, nil, model.AccessPolicyProtected, "secret123", "alice")
	assert.NoError(t, err)

	newHash, err := svc.CommitCollectionWithPasscode(hash, []model.AnonCollectionEntry{
		{Path: "extra.txt", Hash: "b7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a"},
	}, "add extra", "secret123", "alice")
	assert.NoError(t, err)

	committed, err := svc.GetCollectionByHash(newHash)
	assert.NoError(t, err)
	assert.Equal(t, model.AccessPolicyProtected, committed.EffectiveAccessPolicy(), "commit must inherit AccessPolicyProtected")
	assert.Equal(t, "secret123", committed.Passcode, "commit must inherit Passcode")
}

// TestCommitCollection_RequiresPasscodeWhenProtected 发现背景：匿名调用方若对受保护合集执行 commit，
// 必须出示合集的正确口令，防止未授权第三方篡改受保护合集。
func TestCommitCollection_RequiresPasscodeWhenProtected(t *testing.T) {
	tmpDir, svc := setupAnonServiceTest(t)
	defer os.RemoveAll(tmpDir)

	hash, err := svc.CreateCollectionWithPolicy("protected-coll", validEntries(), nil,
		model.VisibilityPublic, nil, model.AccessPolicyProtected, "secret123", "")
	assert.NoError(t, err)

	// 无口令尝试 commit：必须被拒绝
	_, err = svc.CommitCollectionWithPasscode(hash, []model.AnonCollectionEntry{
		{Path: "extra.txt", Hash: "b7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a"},
	}, "add extra", "", "")
	assert.Error(t, err, "anonymous commit without passcode must fail")
	assert.Contains(t, err.Error(), "passcode required or invalid")

	// 错误口令尝试 commit：必须被拒绝
	_, err = svc.CommitCollectionWithPasscode(hash, []model.AnonCollectionEntry{
		{Path: "extra.txt", Hash: "b7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a"},
	}, "add extra", "wrong-passcode", "")
	assert.Error(t, err, "anonymous commit with wrong passcode must fail")
	assert.Contains(t, err.Error(), "passcode required or invalid")

	// 正确口令 commit：成功
	newHash, err := svc.CommitCollectionWithPasscode(hash, []model.AnonCollectionEntry{
		{Path: "extra.txt", Hash: "b7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a"},
	}, "add extra", "secret123", "")
	assert.NoError(t, err, "commit with correct passcode must succeed")
	assert.NotEmpty(t, newHash)
}

// 发现背景 (Issue #338)：合集独立权限支持 PSK（预共享密钥），合集内仅持久化 PSK 的 SHA-256 哈希，
// 访问者出示明文密钥进行 SHA-256 恒定时间命中比对，严禁在任何地方明文存储或返回 PSK。
func TestAnonCollection_PSKVerification(t *testing.T) {
	key := "my-secret-preshared-key-2026"
	coll := &model.AnonCollection{
		Owner:        "alice",
		Visibility:   model.VisibilityPSK,
		AccessPolicy: model.AccessPolicyPSK,
	}
	// 计算并设置 PSKHash
	sum := sha256Hex([]byte(key))
	coll.PSKHash = sum

	// 正确 PSK 命中
	assert.True(t, coll.VerifyPSK(key), "correct PSK must verify successfully")
	// 错误 PSK 失败
	assert.False(t, coll.VerifyPSK("wrong-key"), "wrong PSK must fail verification")
	// 空 PSK 失败
	assert.False(t, coll.VerifyPSK(""), "empty PSK must fail verification")
}

// 发现背景 (Issue #338)：合集支持独立权限控制（公开、指定人员 access_list、PSK 哈希命中、仅自己 private），
// 各种请求者与 PSK 参数传入时必须准确判定访问权限（CanAccess）。
func TestAnonCollection_CanAccessIndependentPolicies(t *testing.T) {
	key := "psk-token-999"
	pskHash := sha256Hex([]byte(key))

	// 1. 公开合集 (Public)
	pubColl := &model.AnonCollection{Visibility: model.VisibilityPublic, AccessPolicy: model.AccessPolicyPublic}
	assert.True(t, pubColl.CanAccess("", ""), "public collection accessible to unauthenticated")
	assert.True(t, pubColl.CanAccess("anyone", ""), "public collection accessible to anyone")

	// 2. 指定人员合集 (Restricted / AccessList)
	restrColl := &model.AnonCollection{
		Owner:      "alice",
		Visibility: model.VisibilityRestricted,
		AccessList: []string{"bob", "carol"},
	}
	assert.True(t, restrColl.CanAccess("alice", ""), "owner should access restricted")
	assert.True(t, restrColl.CanAccess("bob", ""), "whitelisted user bob should access restricted")
	assert.False(t, restrColl.CanAccess("dave", ""), "non-whitelisted user dave should be denied")
	assert.False(t, restrColl.CanAccess("", ""), "unauthenticated request denied for restricted")

	// 3. PSK 合集
	pskColl := &model.AnonCollection{
		Owner:        "alice",
		Visibility:   model.VisibilityPSK,
		AccessPolicy: model.AccessPolicyPSK,
		PSKHash:      pskHash,
	}
	assert.True(t, pskColl.CanAccess("alice", ""), "owner should access PSK collection without key")
	assert.True(t, pskColl.CanAccess("", key), "unauthenticated with matching PSK should access")
	assert.True(t, pskColl.CanAccess("stranger", key), "stranger with matching PSK should access")
	assert.False(t, pskColl.CanAccess("stranger", "wrong"), "stranger with wrong PSK denied")
	assert.False(t, pskColl.CanAccess("", ""), "unauthenticated without PSK denied")

	// 4. 私有合集 (Private)
	privColl := &model.AnonCollection{
		Owner:        "alice",
		Visibility:   model.VisibilityPrivate,
		AccessPolicy: model.AccessPolicyPrivate,
	}
	assert.True(t, privColl.CanAccess("alice", ""), "owner should access private")
	assert.False(t, privColl.CanAccess("bob", ""), "non-owner denied for private")
	assert.False(t, privColl.CanAccess("", ""), "unauthenticated denied for private")
}

// 发现背景 (Issue #338)：合集条目增删改（Add / Update / Remove）必须每次递增版本并生成指向上一个版本的
// prev_sha 指针，形成 Git 式的版本演进链，修改后能完整溯源前序版本。
func TestAnonCollection_EntryCRUDAndPrevSHA(t *testing.T) {
	tmpDir, svc := setupAnonServiceTest(t)
	defer os.RemoveAll(tmpDir)

	initialEntries := []model.AnonCollectionEntry{
		{Path: "file1.txt", Hash: "a7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a"},
	}

	// 1. 创建初始版本 A
	hashA, err := svc.CreateCollectionWithPolicy("my-repo", initialEntries, []string{"v1"},
		model.VisibilityPublic, nil, model.AccessPolicyPublic, "", "alice")
	assert.NoError(t, err)
	collA, err := svc.GetCollectionByHash(hashA)
	assert.NoError(t, err)
	assert.Empty(t, collA.PrevSHA, "initial collection must have empty prev_sha")
	assert.Equal(t, 2, collA.Version)
	assert.Len(t, collA.Entries, 1)

	// 2. 添加条目 (AddCollectionEntry) -> 生成版本 B
	newEntry := model.AnonCollectionEntry{
		Path: "file2.txt",
		Hash: "b7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434b",
	}
	hashB, err := svc.AddCollectionEntry(hashA, newEntry, "alice", "")
	assert.NoError(t, err)
	assert.NotEqual(t, hashA, hashB)

	collB, err := svc.GetCollectionByHash(hashB)
	assert.NoError(t, err)
	assert.Equal(t, hashA, collB.PrevSHA, "version B must have prev_sha pointing to version A")
	assert.Equal(t, 3, collB.Version, "version should increment to 3")
	assert.Len(t, collB.Entries, 2)

	// 3. 更改条目 (UpdateCollectionEntry) -> 生成版本 C
	updatedEntry := model.AnonCollectionEntry{
		Path: "file1.txt",
		Hash: "c7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434c",
	}
	hashC, err := svc.UpdateCollectionEntry(hashB, updatedEntry, "alice", "")
	assert.NoError(t, err)
	assert.NotEqual(t, hashB, hashC)

	collC, err := svc.GetCollectionByHash(hashC)
	assert.NoError(t, err)
	assert.Equal(t, hashB, collC.PrevSHA, "version C must have prev_sha pointing to version B")
	assert.Equal(t, 4, collC.Version, "version should increment to 4")
	assert.Len(t, collC.Entries, 2)
	assert.Equal(t, "c7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434c", collC.Entries[0].GetPrimaryHash())

	// 4. 删除条目 (RemoveCollectionEntry) -> 生成版本 D
	hashD, err := svc.RemoveCollectionEntry(hashC, "file2.txt", "alice", "")
	assert.NoError(t, err)
	assert.NotEqual(t, hashC, hashD)

	collD, err := svc.GetCollectionByHash(hashD)
	assert.NoError(t, err)
	assert.Equal(t, hashC, collD.PrevSHA, "version D must have prev_sha pointing to version C")
	assert.Equal(t, 5, collD.Version, "version should increment to 5")
	assert.Len(t, collD.Entries, 1)
	assert.Equal(t, "file1.txt", collD.Entries[0].Path)
}

// 发现背景 (Issue #338)：验证能够沿 prev_sha 完整回溯版本历史（从 D -> C -> B -> A）。
func TestAnonCollection_HistoryTraversal(t *testing.T) {
	tmpDir, svc := setupAnonServiceTest(t)
	defer os.RemoveAll(tmpDir)

	// 构造 3 个版本链：A -> B -> C
	hashA, err := svc.CreateCollectionWithPolicy("history-repo", []model.AnonCollectionEntry{
		{Path: "init.txt", Hash: "a7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a"},
	}, nil, model.VisibilityPublic, nil, model.AccessPolicyPublic, "", "alice")
	assert.NoError(t, err)

	hashB, err := svc.AddCollectionEntry(hashA, model.AnonCollectionEntry{
		Path: "step2.txt", Hash: "b7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434b",
	}, "alice", "")
	assert.NoError(t, err)

	hashC, err := svc.AddCollectionEntry(hashB, model.AnonCollectionEntry{
		Path: "step3.txt", Hash: "c7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434c",
	}, "alice", "")
	assert.NoError(t, err)

	history, err := svc.GetCollectionHistory(hashC, 10, "alice", "")
	assert.NoError(t, err)
	assert.Len(t, history, 3, "history should contain 3 commits from C back to A")

	// 校验链序
	assert.Equal(t, hashC, history[0].Hash)
	assert.Equal(t, hashB, history[0].PrevSHA)
	assert.Equal(t, hashB, history[1].Hash)
	assert.Equal(t, hashA, history[1].PrevSHA)
	assert.Equal(t, hashA, history[2].Hash)
	assert.Empty(t, history[2].PrevSHA)
}

// 发现背景 (Issue #338)：非 Owner 针对 PSK 保护合集执行增删改条目操作时，必须出示正确 PSK，
// 否则即便拥有当前版本的 SHA 也被严密拦截。
func TestAnonCollection_EntryCRUDRequiresPSK(t *testing.T) {
	tmpDir, svc := setupAnonServiceTest(t)
	defer os.RemoveAll(tmpDir)

	psk := "topsecret123"
	hashA, err := svc.CreateCollectionWithPolicyAndPSK("psk-repo", []model.AnonCollectionEntry{
		{Path: "data.txt", Hash: "a7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a"},
	}, nil, model.VisibilityPSK, nil, model.AccessPolicyPSK, "", psk, "alice")
	assert.NoError(t, err)

	// 陌生人无 PSK 尝试添加条目：拒绝
	_, err = svc.AddCollectionEntry(hashA, model.AnonCollectionEntry{
		Path: "hacked.txt", Hash: "b7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434b",
	}, "", "")
	assert.Error(t, err)

	// 陌生人错误 PSK 尝试添加条目：拒绝
	_, err = svc.AddCollectionEntry(hashA, model.AnonCollectionEntry{
		Path: "hacked.txt", Hash: "b7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434b",
	}, "", "wrong-key")
	assert.Error(t, err)

	// 陌生人正确 PSK 添加条目：成功
	hashB, err := svc.AddCollectionEntry(hashA, model.AnonCollectionEntry{
		Path: "hacked.txt", Hash: "b7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434b",
	}, "", psk)
	assert.NoError(t, err)
	assert.NotEmpty(t, hashB)

	collB, err := svc.GetCollectionByHash(hashB)
	assert.NoError(t, err)
	assert.Equal(t, hashA, collB.PrevSHA)
	assert.True(t, collB.VerifyPSK(psk), "PSKHash must be inherited by version B")
}

// 发现背景 (Issue #338)：通过 UpdateCollectionPermissions 修改权限时，同样必须记录 prev_sha 指针。
func TestAnonCollection_UpdatePermissionsPrevSHA(t *testing.T) {
	tmpDir, svc := setupAnonServiceTest(t)
	defer os.RemoveAll(tmpDir)

	hashA, err := svc.CreateCollectionWithPolicy("perm-repo", validEntries(), nil,
		model.VisibilityPublic, nil, model.AccessPolicyPublic, "", "alice")
	assert.NoError(t, err)

	newHash, err := svc.UpdateCollectionPermissions(hashA, model.VisibilityPSK, nil, model.AccessPolicyPSK, "new-psk-key", "alice")
	assert.NoError(t, err)
	assert.NotEqual(t, hashA, newHash)

	updated, err := svc.GetCollectionByHash(newHash)
	assert.NoError(t, err)
	assert.Equal(t, hashA, updated.PrevSHA, "permission update must link prev_sha to previous version")
	assert.True(t, updated.VerifyPSK("new-psk-key"))
}
