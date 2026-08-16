// CollectionService 集合域用例编排层。
// M2 收层：controller 此前直调 repository（60+ 处散落 collection/fork/merge/file
// 控制器），现在集合读写全部经此收编——controller 只依赖 service，repository
// 只被 service 引用。方法名与 repository 一一对应，仅做透明转发（未来缓存/事务放这里）。
package service

import (
	"peerdrive/internal/model"
	"peerdrive/internal/repository"
)

type CollectionService struct{}

func NewCollectionService() *CollectionService { return &CollectionService{} }

// ─── 查询 ────────────────────────────────────────────────────────────

// Get 按用户名+集合名查集合（未找到返回 nil,nil）。
func (s *CollectionService) Get(username, collectionName string) (*model.Collection, error) {
	return repository.GetCollection(username, collectionName)
}

// List 列出指定用户的全部集合。
func (s *CollectionService) List(username string) ([]model.Collection, error) {
	return repository.ListCollections(username)
}

// Search 在公开集合中模糊搜索。
func (s *CollectionService) Search(q string) ([]model.Collection, error) {
	return repository.SearchCollections(q)
}

// ListPublic 列出全部公开集合，q 非空时按用户名/集合名模糊过滤。
func (s *CollectionService) ListPublic(q string) ([]model.Collection, error) {
	return repository.ListPublicCollections(q)
}

// Create 创建集合，返回新集合 ID。
// followRedirects != nil 时用显式值；tags 非空时带标签。对应 repository 三个
// Create 变体的语义合并（原 controller 三分支）。
func (s *CollectionService) Create(username, collectionName, visibility string, followRedirects *bool, tags []string) (int, error) {
	if followRedirects != nil {
		return repository.CreateCollectionWithFull(username, collectionName, visibility, *followRedirects, tags)
	}
	if len(tags) > 0 {
		return repository.CreateCollectionWithTags(username, collectionName, visibility, tags)
	}
	return repository.CreateCollectionWithVisibility(username, collectionName, visibility)
}

// CreatePlain 创建集合（不带 visibility/tags，fork 流程用）。
func (s *CollectionService) CreatePlain(username, collectionName string) (int, error) {
	return repository.CreateCollection(username, collectionName)
}

// GetOrCreate 查询或创建，返回集合 ID。
func (s *CollectionService) GetOrCreate(username, collectionName string) (int, error) {
	return repository.GetOrCreateCollection(username, collectionName)
}

// SetVisibility 设置可见性。
func (s *CollectionService) SetVisibility(username, collectionName, visibility string) error {
	return repository.SetCollectionVisibility(username, collectionName, visibility)
}

// UpdateTags 更新集合标签。
func (s *CollectionService) UpdateTags(username, collectionName string, tags []string) error {
	return repository.UpdateCollectionTags(username, collectionName, tags)
}

// UpdateCurrentHash 更新当前快照指针（commit 后调用）。
func (s *CollectionService) UpdateCurrentHash(collectionID int, hash string) error {
	return repository.UpdateCurrentHash(collectionID, hash)
}

// ─── 条目 ────────────────────────────────────────────────────────────

// ListEntries 列出集合全部条目。
func (s *CollectionService) ListEntries(collectionID int) ([]model.CollectionEntry, error) {
	return repository.ListCollectionEntries(collectionID)
}

// GetEntry 按 path 查单条目。
func (s *CollectionService) GetEntry(collectionID int, path string) (*model.CollectionEntry, error) {
	return repository.GetCollectionEntry(collectionID, path)
}

// AddEntry 添加条目（只存 hash）。
func (s *CollectionService) AddEntry(collectionID int, path, hash string) error {
	return repository.AddCollectionEntry(collectionID, path, hash)
}

// AddProviderEntry 添加含 providers 的条目。
func (s *CollectionService) AddProviderEntry(collectionID int, path string, providers []model.Provider) error {
	return repository.AddProviderCollectionEntry(collectionID, path, providers)
}

// RemoveEntry 移除条目。
func (s *CollectionService) RemoveEntry(collectionID int, path string) error {
	return repository.RemoveCollectionEntry(collectionID, path)
}

// ─── 版本 ────────────────────────────────────────────────────────────

// CreateVersion 创建版本快照，返回版本 ID 和版本号。
func (s *CollectionService) CreateVersion(collectionID int, commitMsg string, parentVersionID *int) (int, int, error) {
	return repository.CreateVersion(collectionID, commitMsg, parentVersionID)
}

// SnapshotEntries 将集合当前条目快照复制到 version_entries。
func (s *CollectionService) SnapshotEntries(versionID, collectionID int) error {
	return repository.SnapshotVersionEntries(versionID, collectionID)
}

// VersionLog 返回版本历史。
func (s *CollectionService) VersionLog(collectionID int) ([]model.CollectionVersion, error) {
	return repository.GetVersionLog(collectionID)
}

// VersionEntries 返回指定版本快照条目。
func (s *CollectionService) VersionEntries(versionID int) ([]model.VersionEntry, error) {
	return repository.GetVersionEntries(versionID)
}

// RestoreVersion 用旧版本快照覆盖当前条目。
func (s *CollectionService) RestoreVersion(versionID, collectionID int) error {
	return repository.RestoreVersionEntries(versionID, collectionID)
}

// ─── 匿名集合适配（Plaza/AnonExplorer 主链路） ───────────────────────

// GetAnonByHash 读取匿名集合元数据（JSON 存储，含版本校验）。
func (s *CollectionService) GetAnonByHash(hash, storageDir string) (*model.AnonCollection, error) {
	return repository.GetAnonCollectionByHash(hash, storageDir)
}

// SaveAnon 持久化匿名集合（JSON 到磁盘 + file_meta 登记）。
func (s *CollectionService) SaveAnon(coll *model.AnonCollection, storageDir string) (string, error) {
	return repository.SaveCollection(coll, storageDir)
}
