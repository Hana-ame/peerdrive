package pathutil

// Hardlink policy: one check, shared by two call sites (transport.FileIndexService.Create and
// service.FileService.RegisterLocal).
//
// Why both: there is more than one registration entry point. A remote peer calling create via WebRTC
// registers files under the download root; an operator calling register_local via HTTP registers files
// under the storage/shared directory. Both paths can register a file that lives inside the allowed root
// but also has another name outside. Previously only the transport side checked, leaving a hole on the
// HTTP side.

import (
	"fmt"
	"os"
)

// HardlinkCheckEnabled returns whether this platform actually checks for hard links.
//
// Both conditions are required: the platform must be able to report link counts, and the operator
// has not explicitly opted out.
// Windows was always false before (os.FileInfo had no nlink); after switching to handle-based
// retrieval it is consistent with Unix.
func HardlinkCheckEnabled() bool {
	return NlinkSupported() && os.Getenv("PEERDRIVE_ALLOW_HARDLINKS") != "1"
}

// RejectHardlink rejects regular files that have "more than one name".
//
// Hard links have no direction: the same inode may have one name inside the allowed root and another
// outside, and there is **no** way to determine from the path alone whether it is exposed (EvalSymlinks
// doesn't catch it either). The only policy possible is "link count > 1 -> reject" -- less is more.
//
// False-positive scenarios: pnpm's node_modules, git alternates, `cp -l` backups, and similar
// directories make heavy use of hard links. To register them, set PEERDRIVE_ALLOW_HARDLINKS=1.
//
// The argument must be the **already-opened *os.File**, not a FileInfo: on Windows, NumberOfLinks can
// only be obtained via GetFileInformationByHandle on the handle, and the FileInfo path never returns
// a usable answer (before 2026-09-20 the Windows defense was therefore empty). This also avoids the
// window where the file is swapped between stat and open.
func RejectHardlink(path string, f *os.File) error {
	if !HardlinkCheckEnabled() || f == nil {
		return nil
	}
	fi, err := f.Stat()
	if err != nil {
		return nil // If attributes can't be obtained, link count can't be discussed; treat as "unknown"
	}
	if !fi.Mode().IsRegular() {
		return nil
	}
	if n := NlinkOf(f); n > 1 {
		return fmt.Errorf("%s has %d hard links: refusing to register a file that may have a name outside the allowed root (set PEERDRIVE_ALLOW_HARDLINKS=1 to allow)", path, n)
	}
	return nil
}
