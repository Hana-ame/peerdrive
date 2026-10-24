package p2p_bt

import (
	"fmt"
	"os"
	"path/filepath"
)

// ---- Progress queries ----

func (c *BTClient) GetDownload(infohash string) *DownloadStatus {
	c.mu.RLock()
	ds, ok := c.downloads[infohash]
	c.mu.RUnlock()
	if !ok {
		return nil
	}
	// Hold the read lock while building status to prevent data races
	// with PauseDownload/ResumeDownload which write ds.status without
	// holding the write lock.
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.buildStatus(ds)
}

func (c *BTClient) ListDownloads() []DownloadStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make([]DownloadStatus, 0, len(c.downloads))
	for _, ds := range c.downloads {
		result = append(result, *c.buildStatus(ds))
	}
	return result
}

func (c *BTClient) buildStatus(ds *downloadState) *DownloadStatus {
	t := ds.t
	stats := t.Stats()

	totalSize := t.Length()
	piecesTotal := 0
	if info := t.Info(); info != nil {
		piecesTotal = info.NumPieces()
	}

	errStr := ""
	if ds.err != nil {
		errStr = ds.err.Error()
	}

	return &DownloadStatus{
		InfoHash:    ds.infoHashHex,
		Name:        ds.name,
		TotalSize:   totalSize,
		Downloaded:  t.BytesCompleted(),
		PiecesTotal: piecesTotal,
		PiecesDone:  stats.PiecesComplete,
		Peers:       stats.TotalPeers,
		Speed:       0,
		Status:      ds.status,
		Error:       errStr,
		Seeding:     ds.seeding,
	}
}

// ---- Pause/Resume ----

func (c *BTClient) PauseDownload(infohash string) error {
	c.mu.Lock()
	ds, ok := c.downloads[infohash]
	if !ok {
		c.mu.Unlock()
		return fmt.Errorf("download %s not found", infohash)
	}

	if ds.status != "downloading" {
		c.mu.Unlock()
		return fmt.Errorf("download %s is not in progress (status=%s)", infohash, ds.status)
	}

	ds.t.DisallowDataDownload()
	ds.status = "paused"
	c.mu.Unlock()
	LogInfo("bt-client: paused download %s (%s)", infohash, ds.name)
	return nil
}

func (c *BTClient) ResumeDownload(infohash string) error {
	c.mu.Lock()
	ds, ok := c.downloads[infohash]
	if !ok {
		c.mu.Unlock()
		return fmt.Errorf("download %s not found", infohash)
	}

	if ds.status != "paused" {
		c.mu.Unlock()
		return fmt.Errorf("download %s is not paused (status=%s)", infohash, ds.status)
	}

	ds.t.AllowDataDownload()
	ds.status = "downloading"
	c.mu.Unlock()
	LogInfo("bt-client: resumed download %s (%s)", infohash, ds.name)
	return nil
}

// ---- Removal ----

func (c *BTClient) RemoveDownload(infohash string) error {
	c.mu.Lock()
	ds, ok := c.downloads[infohash]
	if !ok {
		c.mu.Unlock()
		return fmt.Errorf("download %s not found", infohash)
	}
	delete(c.downloads, infohash)
	delete(c.torrentData, infohash)
	delete(c.autoSeed, infohash)
	c.mu.Unlock()

	ds.t.Drop()

	// Remove the data directory
	dataDir := filepath.Join(c.dataDir, ds.name)
	if dataDir != "" && dataDir != c.dataDir {
		if err := os.RemoveAll(dataDir); err != nil {
			LogWarn("bt-client: remove data dir %s: %v", dataDir, err)
		}
	}

	LogInfo("bt-client: removed download %s (%s)", infohash, ds.name)
	return nil
}

// ---- Global statistics ----

func (c *BTClient) GetGlobalStats() *GlobalStats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	stats := &GlobalStats{}
	if globalDHT != nil {
		stats.DHTNodes = globalDHT.NumNodes()
	}

	for _, ds := range c.downloads {
		switch ds.status {
		case "downloading":
			stats.ActiveTorrents++
		case "paused":
			stats.PausedTorrents++
		case "completed":
			stats.Completed++
		case "error":
			stats.Errors++
		default:
			stats.ActiveTorrents++
		}
		stats.TotalDown += ds.t.BytesCompleted()
	}

	return stats
}
