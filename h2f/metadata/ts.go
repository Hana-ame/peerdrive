package metadata

import (
	"io/fs"
	"time"
)

// timestamps are the four times FileMetadata carries.
type timestamps struct {
	Ctime time.Time
	Mtime time.Time
	Btime time.Time
	Atime time.Time
}

// utc normalises every field. This is not cosmetic: the same values are hashed
// into the parent directory's manifest, and a Time in a local zone would render
// as "+08:00" in JSON while the database round trip produces "Z" - the same
// directory would then have two different hashes depending on whether it was
// just scanned or read back.
func (t timestamps) utc() timestamps {
	t.Ctime = t.Ctime.UTC()
	t.Mtime = t.Mtime.UTC()
	t.Btime = t.Btime.UTC()
	t.Atime = t.Atime.UTC()
	return t
}

// times reads the timestamps of path. Where the platform cannot report one,
// it stays zero: an unknown Ctime is honest, a substituted one is not.
func times(path string, fi fs.FileInfo) timestamps {
	return platformTimes(path, fi).utc()
}
