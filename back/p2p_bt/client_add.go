package p2p_bt

import (
	"bytes"
	"fmt"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
)

// ---- Adding downloads ----

// AddTorrentBytes starts a download from raw .torrent file bytes, returning the parsed metadata.
func (c *BTClient) AddTorrentBytes(data []byte) (*TorrentMeta, error) {
	mi, err := metainfo.Load(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("invalid torrent file: %w", err)
	}

	infoHash := mi.HashInfoBytes()
	ih := infoHash.HexString()

	c.mu.Lock()
	if _, exists := c.downloads[ih]; exists {
		c.mu.Unlock()
		return nil, fmt.Errorf("torrent %s already added", ih)
	}
	c.mu.Unlock()

	// Inject public trackers if the torrent has no announce list.
	// This ensures the client can find peers even for torrents with zero
	// trackers (common in private or older torrents).
	c.injectPublicTrackers(mi)

	t, err := c.cl.AddTorrent(mi)
	if err != nil {
		return nil, fmt.Errorf("add torrent: %w", err)
	}

	// Wait for metadata to be available (.torrent files are typically available immediately)
	<-t.GotInfo()

	meta := torrentMetaFromLibrary(mi, t)

	ds := &downloadState{
		infoHashHex: ih,
		name:        meta.Name,
		t:           t,
		status:      "downloading",
		startTime:   time.Now(),
		doneCh:      make(chan struct{}),
	}

	c.mu.Lock()
	c.downloads[ih] = ds
	c.torrentData[ih] = data
	c.mu.Unlock()

	t.DownloadAll()
	go c.watchDownload(ds)

	LogInfo("bt-client: AddTorrentBytes infohash=%s name=%q", ih, meta.Name)
	return meta, nil
}

// AddMagnetURI starts a download from a magnet link URI.
func (c *BTClient) AddMagnetURI(uri string) (*TorrentMeta, error) {
	spec, err := torrent.TorrentSpecFromMagnetUri(uri)
	if err != nil {
		return nil, fmt.Errorf("invalid magnet URI: %w", err)
	}

	ih := spec.InfoHash.HexString()

	c.mu.Lock()
	if _, exists := c.downloads[ih]; exists {
		c.mu.Unlock()
		return nil, fmt.Errorf("torrent %s already added", ih)
	}
	c.mu.Unlock()

	// Inject public trackers into the magnet spec if no trackers are present.
	c.injectPublicTrackersToSpec(spec)

	t, _, err := c.cl.AddTorrentSpec(spec)
	if err != nil {
		return nil, fmt.Errorf("add magnet: %w", err)
	}

	name := spec.DisplayName
	if name == "" {
		name = ih
	}

	ds := &downloadState{
		infoHashHex: ih,
		name:        name,
		t:           t,
		status:      "downloading",
		startTime:   time.Now(),
		doneCh:      make(chan struct{}),
	}

	c.mu.Lock()
	c.downloads[ih] = ds
	c.mu.Unlock()

	// For magnet links, download only after metadata is available.
	go func() {
		select {
		case <-t.GotInfo():
			t.DownloadAll()
		case <-time.After(5 * time.Minute):
			LogWarn("bt-client: magnet metadata timeout for %s", ih)
			return
		}
	}()

	go c.watchDownload(ds)

	LogInfo("bt-client: AddMagnetURI infohash=%s name=%q", ih, name)

	meta := &TorrentMeta{
		InfoHashHex: ih,
		Name:        name,
	}
	if len(spec.Trackers) > 0 {
		for _, tier := range spec.Trackers {
			meta.AnnounceList = append(meta.AnnounceList, tier...)
		}
	}

	return meta, nil
}

// injectPublicTrackers modifies the MetaInfo's announce list in-place,
// adding public trackers if the torrent has no existing announce list.
// If the torrent already has trackers, no change is made — this avoids
// duplicating endpoints and respects private tracker configurations.
func (c *BTClient) injectPublicTrackers(mi *metainfo.MetaInfo) {
	if len(c.publicTrackers) == 0 {
		return
	}
	// Convert existing announce list to flat []string.
	var existing []string
	if mi.AnnounceList.OverridesAnnounce(mi.Announce) {
		for _, tier := range mi.AnnounceList {
			existing = append(existing, tier...)
		}
	} else if mi.Announce != "" {
		existing = []string{mi.Announce}
	}
	merged := mergePublicTrackers(existing, c.publicTrackers)
	if len(merged) == len(existing) {
		return // no change
	}
	// Put the merged list into a single tier (tier order doesn't matter
	// for anacrolix's concurrent tracker contact).
	mi.AnnounceList = metainfo.AnnounceList{merged}
	mi.Announce = "" // AnnounceList takes precedence when non-empty.
}

// injectPublicTrackersToSpec modifies a TorrentSpec's tracker list in-place,
// adding public trackers if the magnet URI has no existing trackers.
func (c *BTClient) injectPublicTrackersToSpec(spec *torrent.TorrentSpec) {
	if len(c.publicTrackers) == 0 {
		return
	}
	var existing []string
	for _, tier := range spec.Trackers {
		existing = append(existing, tier...)
	}
	merged := mergePublicTrackers(existing, c.publicTrackers)
	if len(merged) == len(existing) {
		return // no change
	}
	spec.Trackers = [][]string{merged}
}

// AddTorrent is a backward-compatible method — adds a download from a TorrentMeta.
// Since the old TorrentMeta does not contain the raw .torrent bytes, this method only reconstructs a magnet link and adds it.
// New code should use AddTorrentBytes.
func (c *BTClient) AddTorrent(meta *TorrentMeta) error {
	uri := fmt.Sprintf("magnet:?xt=urn:btih:%s", meta.InfoHashHex)
	if meta.Name != "" {
		uri += "&dn=" + meta.Name
	}
	for _, tr := range meta.AnnounceList {
		uri += "&tr=" + tr
	}
	_, err := c.AddMagnetURI(uri)
	return err
}

// AddMagnet is a backward-compatible method — adds a download from a MagnetInfo.
// New code should use AddMagnetURI.
func (c *BTClient) AddMagnet(magnet *MagnetInfo) error {
	uri := fmt.Sprintf("magnet:?xt=urn:btih:%s", magnet.InfoHash)
	if magnet.DisplayName != "" {
		uri += "&dn=" + magnet.DisplayName
	}
	for _, tr := range magnet.Trackers {
		uri += "&tr=" + tr
	}
	_, err := c.AddMagnetURI(uri)
	return err
}
