package metadata

import (
	"encoding/json"
	"fmt"

	"github.com/Hana-ame/peerdrive/interface"
)

// encodeManifest returns the canonical bytes of a directory's content: the JSON
// array of its children's absolute paths, in the order they were handed over.
//
//	["/data/photos/b.jpg", "/data/photos/a.jpg"]
//
// 存路径，不存哈希：目录记的是"这一层挂了哪些位置"。于是子文件改了内容，
// 父目录的 hash 不变——身份逐层下放到文件自己；只有增、删、改名、搬家这类
// 改变目录结构的事，才会顶起它。
//
// 这里不排序。顺序由调用方决定，Index 交进来的是扫描顺序（os.ReadDir，
// 按文件名升序），所以同一棵树重复扫描仍然逐字节一致；换成别的来源时，
// 顺序就是那个来源给的顺序。
//
// 空目录编码成 "[]"，不是 "null"。
func encodeManifest(children []iface.FileMetadata) ([]byte, error) {
	paths := make([]string, len(children))
	for i, child := range children {
		paths[i] = child.FullPath
	}

	b, err := json.Marshal(paths)
	if err != nil {
		return nil, fmt.Errorf("metadata: encode manifest: %w", err)
	}
	return b, nil
}
