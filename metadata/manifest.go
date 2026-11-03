package metadata

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/Hana-ame/peerdrive/interface"
)

// encodeManifest returns the canonical bytes of a directory's content: the
// JSON encoding of its direct children, ordered by Ctime descending.
//
// Ctime is unique per inode, so ties only happen for files created inside the
// same filesystem clock tick. Filename (then FullPath) breaks those
// deterministically, otherwise the same tree could hash differently between
// runs and the directory would lose its identity.
//
// An empty directory must encode as "[]", never "null": the two are different
// contents and would give different hashes.
func encodeManifest(children []iface.FileMetadata) ([]byte, error) {
	ordered := orderedByCtimeDesc(children)
	b, err := json.Marshal(ordered)
	if err != nil {
		return nil, fmt.Errorf("metadata: encode manifest: %w", err)
	}
	return b, nil
}

func orderedByCtimeDesc(children []iface.FileMetadata) []iface.FileMetadata {
	ordered := make([]iface.FileMetadata, len(children))
	copy(ordered, children)
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if !a.Ctime.Equal(b.Ctime) {
			return a.Ctime.After(b.Ctime)
		}
		if a.Filename != b.Filename {
			return a.Filename < b.Filename
		}
		return a.FullPath < b.FullPath
	})
	return ordered
}
