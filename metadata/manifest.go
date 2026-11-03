package metadata

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/Hana-ame/peerdrive/interface"
)

// encodeManifest returns the canonical bytes of a directory's content: the JSON
// array of its children's absolute paths, sorted as strings.
//
//	["/data/photos/a.jpg", "/data/photos/raw"]
//
// 存路径，不存哈希：目录记的是"这一层挂了哪些位置"。于是子文件改了内容，
// 父目录的 hash 不变——身份逐层下放到文件自己；只有增、删、改名、搬家这类
// 改变目录结构的事，才会顶起它。
//
// 排序用字典序，和扫描顺序无关；空目录编码成 "[]"，不是 "null"。
func encodeManifest(children []iface.FileMetadata) ([]byte, error) {
	paths := make([]string, len(children))
	for i, child := range children {
		paths[i] = child.FullPath
	}
	sort.Strings(paths)

	b, err := json.Marshal(paths)
	if err != nil {
		return nil, fmt.Errorf("metadata: encode manifest: %w", err)
	}
	return b, nil
}
