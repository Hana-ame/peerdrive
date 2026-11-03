package metadata

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/Hana-ame/peerdrive/interface"
)

// encodeManifest returns the canonical bytes of a directory's content: the JSON
// array of its children's SHA256, sorted as strings.
//
// 身份只看内容，不看摆放：时间戳、绝对路径、子项从哪个方向扫出来，都不进哈希。
// 同一棵树复制到别的路径、换台机器，hash 不变。排序用字典序就够了——哈希本身
// 没有先后，任何全序都能把"同一批哈希"归一到同一个字节串。
//
// 空目录编码成 "[]"，不是 "null"：两者是不同的内容，hash 也不同。
func encodeManifest(children []iface.FileMetadata) ([]byte, error) {
	hashes := make([]string, len(children))
	for i, child := range children {
		hashes[i] = child.SHA256
	}
	sort.Strings(hashes)

	b, err := json.Marshal(hashes)
	if err != nil {
		return nil, fmt.Errorf("metadata: encode manifest: %w", err)
	}
	return b, nil
}
