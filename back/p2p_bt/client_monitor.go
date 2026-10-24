package p2p_bt

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"time"
)

// ---- Download completion monitoring ----

func (c *BTClient) watchDownload(ds *downloadState) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		c.mu.RLock()
		_, exists := c.downloads[ds.infoHashHex]
		curStatus := ds.status // read under lock to avoid race with Pause/Resume
		c.mu.RUnlock()
		if !exists {
			return
		}

		t := ds.t
		select {
		case <-t.Closed():
			return
		default:
		}

		length := t.Length()
		if length > 0 && t.BytesCompleted() >= length && curStatus == "downloading" {
			c.finalizeDownload(ds)
			return
		}
	}
}

func (c *BTClient) finalizeDownload(ds *downloadState) {
	t := ds.t
	dataRoot := filepath.Join(c.dataDir, t.Name())

	// I/O work: read files and compute hashes (no lock needed).
	var completedFiles []CompletedFile
	for _, f := range t.Files() {
		var absPath string
		if info := t.Info(); info != nil && !info.IsDir() {
			// Single-file torrent: the file IS the torrent name.
			absPath = dataRoot
		} else {
			// Multi-file torrent: files are under the torrent directory.
			absPath = filepath.Join(dataRoot, f.Path())
		}

		fi, err := os.Stat(absPath)
		if err != nil {
			LogWarn("bt-client: stat completed file %q: %v", absPath, err)
			continue
		}

		data, err := os.ReadFile(absPath)
		if err != nil {
			LogWarn("bt-client: read completed file %q: %v", absPath, err)
			continue
		}

		hash := sha256.Sum256(data)
		completedFiles = append(completedFiles, CompletedFile{
			InfoHash: ds.infoHashHex,
			Path:     absPath,
			Size:     fi.Size(),
			SHA256:   hex.EncodeToString(hash[:]),
		})
	}

	// State update under write lock to avoid races with Pause/Resume/GetDownload.
	c.mu.Lock()
	ds.completed = true
	ds.status = "completed"
	ds.files = completedFiles

	if ds.doneCh != nil {
		close(ds.doneCh)
	}

	shouldAutoSeed := false
	if c.autoSeed[ds.infoHashHex] {
		shouldAutoSeed = true
	}
	c.mu.Unlock()

	if shouldAutoSeed {
		c.mu.Lock()
		LogInfo("bt-client: auto-seeding %s", ds.infoHashHex)
		ds.t.AllowDataUpload()
		ds.seeding = true
		ds.status = "seeding"
		c.mu.Unlock()
	}

	if c.onComplete != nil && len(completedFiles) > 0 {
		LogInfo("bt-client: firing onComplete for %s (%d files)", ds.infoHashHex, len(completedFiles))
		c.onComplete(ds.infoHashHex, completedFiles)
	}

	LogInfo("bt-client: download completed infohash=%s name=%q files=%d",
		ds.infoHashHex, ds.name, len(completedFiles))
}
