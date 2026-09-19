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

// 发现背景：前端「广播」改成三选项（公开/指定权限/仅自己），限定的集会能不能被别人看到
// 全靠 CanView；而 CanView 最容易写错的一点是把「没有账号（未认证请求）」当成放行。
func TestAnonCollection_CanView(t *testing.T) {
	coll := &model.AnonCollection{Owner: "alice", Visibility: model.VisibilityRestricted, AccessList: []string{"bob"}}

	assert.True(t, coll.CanView("bob"), "名单内账号应放行")
	assert.True(t, coll.CanView("alice"), "owner 即使在名单外也应放行")
	assert.False(t, coll.CanView("carol"), "名单外账号应拒绝")
	assert.False(t, coll.CanView(""), "未认证请求不能穿透 restricted")

	privateColl := &model.AnonCollection{Owner: "alice", Visibility: model.VisibilityPrivate}
	assert.True(t, privateColl.CanView("alice"))
	assert.False(t, privateColl.CanView("bob"))
	assert.False(t, privateColl.CanView(""))

	// 历史集合没有 visibility 字段（老版本写入的 JSON）→ 兜底 public
	legacy := &model.AnonCollection{Owner: "alice"}
	assert.Equal(t, model.VisibilityPublic, legacy.EffectiveVisibility())
	assert.True(t, legacy.CanView(""))
}

// 发现背景：restricted 没带名单时集合变成「谁都看不到」，属于用户在 UI 上切了选项却忘了选人的典型误操作，
// 必须在服务层直接拦下，而不是静默生成一个没人能打开的 hash。
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

// 发现背景：集合是 content-addressed，权限写进 JSON 就参与摘要 —— 切权限必然换新 hash。
// 曾经担心的是「旧 hash 还能不能读」，结论是能，且反映的是旧权限（两份并存），这里锁住这个语义。
func TestUpdateCollectionVisibility_ProducesNewHash(t *testing.T) {
	tmpDir, svc := setupAnonServiceTest(t)
	defer os.RemoveAll(tmpDir)

	hash, err := svc.CreateCollectionWithVisibility("c", validEntries(), nil, model.VisibilityPublic, nil, "alice")
	assert.NoError(t, err)

	newHash, err := svc.UpdateCollectionVisibility(hash, model.VisibilityPrivate, nil, "alice")
	assert.NoError(t, err)
	assert.NotEqual(t, hash, newHash, "换权限必须产生新 hash")

	updated, err := svc.GetCollectionByHash(newHash)
	assert.NoError(t, err)
	assert.Equal(t, model.VisibilityPrivate, updated.EffectiveVisibility())
	assert.False(t, updated.CanView("bob"))

	// 旧 hash 仍然解析成旧权限（快照语义，不是原地改）
	old, err := svc.GetCollectionByHash(hash)
	assert.NoError(t, err)
	assert.Equal(t, model.VisibilityPublic, old.EffectiveVisibility())
}

// 发现背景：拿到 hash 的第三方不该能改写别人的权限档位。content-addressed 存储没有 ACL 表，
// 唯一的归属依据就是 Owner 字段，必须显式比对。
func TestUpdateCollectionVisibility_OwnershipEnforced(t *testing.T) {
	tmpDir, svc := setupAnonServiceTest(t)
	defer os.RemoveAll(tmpDir)

	hash, err := svc.CreateCollectionWithVisibility("c", validEntries(), nil, model.VisibilityPublic, nil, "alice")
	assert.NoError(t, err)

	_, err = svc.UpdateCollectionVisibility(hash, model.VisibilityPrivate, nil, "bob")
	assert.Error(t, err, "非 owner 不能改可见性")

	_, err = svc.UpdateCollectionVisibility(hash, model.VisibilityPrivate, nil, "")
	assert.Error(t, err, "未认证请求不能改可见性")
}

// 发现背景：GetCollectionVisibleTo 是给远端（未来 regserver 认证的 peer）读集合的唯一入口，
// 越权必须表现成 404 而不是 403 —— 否则等于替攻击者确认了「这个 hash 存在且属于别人」。
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

// 发现背景（2026-09-19）：commit 会滚动出新 hash，而新集合曾是「只带
// version/name/entries/tags」的裸集合 —— 一个 restricted 合集只要被 commit 一次
// 就静默变回 public（内容随新 hash 对外敞开）。这里锁住权限继承 + 越权拒绝。
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
	assert.NotEqual(t, hash, newHash, "commit 必须产生新 hash")

	committed, err := svc.GetCollectionByHash(newHash)
	assert.NoError(t, err)
	assert.Equal(t, model.VisibilityRestricted, committed.EffectiveVisibility(), "commit 不能把 restricted 降级成 public")
	assert.Equal(t, []string{"bob"}, committed.AccessList, "放行名单必须继承")
	assert.Equal(t, "alice", committed.Owner, "owner 必须继承")
	assert.False(t, committed.CanView("carol"), "继承后的权限仍然拒绝名单外账号")
	assert.True(t, committed.CanView("bob"))
}

// 发现背景（同上批次）：commit 的入口在校验权限之前不能读到源集合——否则
// 拿到 hash 的人可以 commit 别人的 private 合集，再借「新集合」把自己写成 owner。
func TestCommitCollection_DeniedForNonViewer(t *testing.T) {
	tmpDir, svc := setupAnonServiceTest(t)
	defer os.RemoveAll(tmpDir)

	hash, err := svc.CreateCollectionWithVisibility("c", validEntries(), nil,
		model.VisibilityPrivate, nil, "alice")
	assert.NoError(t, err)

	_, err = svc.CommitCollection(hash, nil, "", "bob")
	assert.Error(t, err, "非 owner 不能 commit private 合集")
	assert.Contains(t, err.Error(), "source collection not found")
}

// 发现背景（2026-09-19）：controller 的注释一直写着「entries 里空 hash = 删除条目」，
// 但服务层沿用创建时的 providers 校验把空 providers 直接判非法 → 删除这条路径
// 永远走不到（removeEmpty 是死代码）。这里锁住「空 providers/空 hash = 删除」的语义，
// 同时确认「只设 Hash 不设 Providers」会被归一化成新增而不是误删。
func TestCommitCollection_EmptyHashRemovesEntry(t *testing.T) {
	tmpDir, svc := setupAnonServiceTest(t)
	defer os.RemoveAll(tmpDir)

	hash, err := svc.CreateCollectionWithVisibility("c", validEntries(), nil,
		model.VisibilityPublic, nil, "alice")
	assert.NoError(t, err)

	// 只设 Hash（旧格式调用方）→ 归一化成新增，不能变成删除
	addHash, err := svc.CommitCollection(hash, []model.AnonCollectionEntry{
		{Path: "second.txt", Hash: "b7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a"},
	}, "add", "alice")
	assert.NoError(t, err)
	added, err := svc.GetCollectionByHash(addHash)
	assert.NoError(t, err)
	assert.Len(t, added.Entries, 2, "旧格式 hash 字段应被归一化成 provider 并新增")

	// 空 hash / 空 providers → 删除该条目
	delHash, err := svc.CommitCollection(addHash, []model.AnonCollectionEntry{
		{Path: "second.txt"},
	}, "remove", "alice")
	assert.NoError(t, err)
	deleted, err := svc.GetCollectionByHash(delHash)
	assert.NoError(t, err)
	assert.Len(t, deleted.Entries, 1, "空 hash 应删除对应条目")
	assert.Equal(t, "README.md", deleted.Entries[0].Path)
}
