package metadata

import (
	"fmt"

	"github.com/Hana-ame/peerdrive/h2f/interface"
)

// Collection resolves a list of registered paths to their persisted metadata.
//
// 为什么集合只存路径：refactor 分支的 collection_entries 是 path→hash 两列，
// 因为那时文件索引与集合是两套存储。拆分后 h2f 的 files 表本身以 full_path
// 为主键——一个路径同一时刻只有一份内容，hash 可以由路径查出来。再把 hash
// 存进集合只是多一份会漂移的事实。
//
// 顺序即集合顺序：返回的 []FileMetadata 与入参 paths 一一对应，调用方不需要
// 自己再排。重复路径按重复返回。
//
// 任何一条路径不在表里就整体失败并带出那条路径，而不是跳过——静默跳过会让
// 调用方拿到一个短了一截却看不出短在哪的集合。
//
// 为什么不拼一条 IN 查询：这里是本机 SQLite 的主键查询，集合规模小，循环足够
// 直白；IN 还要处理 SQLite 变量上限和「去重后按原序回填」，复杂度不值得。
func (s *Store) Collection(paths []string) ([]iface.FileMetadata, error) {
	out := make([]iface.FileMetadata, 0, len(paths))
	for _, p := range paths {
		abs, err := normalizePath(p)
		if err != nil {
			return nil, err
		}
		m, err := s.byPath(abs)
		if err != nil {
			return nil, fmt.Errorf("metadata: collection: %w", err)
		}
		out = append(out, m)
	}
	return out, nil
}

// Collection resolves paths using the default store.
func Collection(paths []string) ([]iface.FileMetadata, error) {
	s, err := Default()
	if err != nil {
		return nil, err
	}
	return s.Collection(paths)
}
