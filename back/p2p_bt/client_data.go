package p2p_bt

import (
	"bytes"
	"fmt"
)

// ---- Torrent data and Magnet ----

// GetTorrentBytes returns the raw .torrent file bytes. For downloads added from a magnet link, exports from the library if metadata is available.
func (c *BTClient) GetTorrentBytes(infohash string) ([]byte, error) {
	c.mu.RLock()
	data, ok := c.torrentData[infohash]
	c.mu.RUnlock()
	if ok {
		return data, nil
	}

	c.mu.RLock()
	ds, dsOk := c.downloads[infohash]
	c.mu.RUnlock()
	if !dsOk {
		return nil, fmt.Errorf("download not found: %s", infohash)
	}

	mi := ds.t.Metainfo()
	if len(mi.InfoBytes) == 0 {
		return nil, fmt.Errorf("metadata not yet available for %s", infohash)
	}
	var buf bytes.Buffer
	if err := mi.Write(&buf); err != nil {
		return nil, fmt.Errorf("failed to encode torrent: %w", err)
	}
	return buf.Bytes(), nil
}

// GetMagnetURI generates a magnet link URI for the specified download.
func (c *BTClient) GetMagnetURI(infohash string) string {
	c.mu.RLock()
	ds, ok := c.downloads[infohash]
	c.mu.RUnlock()
	if !ok {
		return ""
	}

	uri := fmt.Sprintf("magnet:?xt=urn:btih:%s", ds.infoHashHex)
	if ds.name != "" && ds.name != ds.infoHashHex {
		uri += "&dn=" + ds.name
	}
	mi := ds.t.Metainfo()
	if len(mi.InfoBytes) > 0 {
		for _, tier := range mi.UpvertedAnnounceList() {
			for _, tr := range tier {
				uri += "&tr=" + tr
			}
		}
	}
	return uri
}

// SetAutoSeed marks the specified infohash to auto-start seeding after download completion.
func (c *BTClient) SetAutoSeed(infohash string) {
	c.mu.Lock()
	c.autoSeed[infohash] = true
	c.mu.Unlock()
}

// HasTorrentBytes checks whether torrent bytes are stored for the specified infohash.
func (c *BTClient) HasTorrentBytes(infohash string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.torrentData[infohash]
	return ok
}

// ---- Seeding ----

func (c *BTClient) StartSeed(infohash string) error {
	c.mu.Lock()
	ds, ok := c.downloads[infohash]
	if !ok {
		c.mu.Unlock()
		return fmt.Errorf("download %s not found", infohash)
	}

	if ds.status != "completed" {
		c.mu.Unlock()
		return fmt.Errorf("download %s has status %q, need 'completed' to seed", infohash, ds.status)
	}
	if ds.seeding {
		c.mu.Unlock()
		return fmt.Errorf("already seeding %s", infohash)
	}

	ds.t.AllowDataUpload()
	ds.seeding = true
	c.mu.Unlock()

	LogInfo("bt-client: started seeding %s (%s)", infohash, ds.name)
	return nil
}

func (c *BTClient) StopSeed(infohash string) error {
	c.mu.Lock()
	ds, ok := c.downloads[infohash]
	if !ok {
		c.mu.Unlock()
		return fmt.Errorf("download %s not found", infohash)
	}

	if !ds.seeding {
		c.mu.Unlock()
		return fmt.Errorf("not currently seeding %s", infohash)
	}

	ds.t.DisallowDataUpload()
	ds.seeding = false
	c.mu.Unlock()

	LogInfo("bt-client: stopped seeding %s", infohash)
	return nil
}

func (c *BTClient) IsSeeding(infohash string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	ds, ok := c.downloads[infohash]
	return ok && ds.seeding
}

func (c *BTClient) ListSeeders() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	keys := make([]string, 0)
	for k, ds := range c.downloads {
		if ds.seeding {
			keys = append(keys, k)
		}
	}
	return keys
}

// ---- Peer manual management ----

func (c *BTClient) AddPeer(infohash, addr string) {
	c.mu.Lock()
	c.customPeers[infohash] = append(c.customPeers[infohash], addr)
	c.mu.Unlock()

	LogInfo("bt-client: AddPeer infohash=%s addr=%s", infohash, addr)
}

func (c *BTClient) GetCustomPeers(infohash string) []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	peers := c.customPeers[infohash]
	result := make([]string, len(peers))
	copy(result, peers)
	return result
}
