//go:build !linux && !windows

package metadata

import (
	"io/fs"
)

// platformTimes is the portable floor: mtime is all os.FileInfo promises.
// Other systems get Mtime and three unknown fields rather than a guess.
func platformTimes(_ string, fi fs.FileInfo) timestamps {
	return timestamps{Mtime: fi.ModTime()}
}
