package p2p_bt

import (
	"encoding/base32"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"

	"peerdrive/internal/log"
)

// MagnetInfo holds parsed data from a BitTorrent magnet URI.
type MagnetInfo struct {
	InfoHash    string   `json:"infohash"`    // 40-char lowercase hex
	InfoHashRaw []byte   `json:"-"`           // 20 bytes
	DisplayName string   `json:"display_name,omitempty"`
	Trackers    []string `json:"trackers,omitempty"`
}

// ParseMagnet parses a magnet URI of the form:
//
//	magnet:?xt=urn:btih:<infohash>&dn=<name>&tr=<tracker>&tr=<tracker>
//
// The infohash may be 40-char hex or 32-char base32.
func ParseMagnet(uri string) (*MagnetInfo, error) {
	defer log.LogDuration("BT.ParseMagnet")()
	log.LogDebug("bt-magnet: ParseMagnet uri=%s", uri)

	if !strings.HasPrefix(uri, "magnet:") {
		return nil, fmt.Errorf("magnet: URI must start with 'magnet:'")
	}

	// Parse query string from the magnet URI.
	// Strip the "magnet:" scheme prefix.
	rawQuery := uri
	if idx := strings.IndexByte(uri, '?'); idx >= 0 {
		rawQuery = uri[idx+1:]
	} else {
		return nil, fmt.Errorf("magnet: missing query string")
	}

	// Split by & and manually parse (url.ParseQuery handles most cases)
	// but we need the raw values without further decoding of the xt parameter.
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return nil, fmt.Errorf("magnet: parse query: %w", err)
	}

	m := &MagnetInfo{}

	// Extract xt (exact topic) — the infohash.
	xts := values["xt"]
	if len(xts) == 0 {
		return nil, fmt.Errorf("magnet: missing xt parameter")
	}

	var infoHashStr string
	for _, xt := range xts {
		if strings.HasPrefix(xt, "urn:btih:") {
			infoHashStr = strings.TrimPrefix(xt, "urn:btih:")
			break
		}
	}
	if infoHashStr == "" {
		return nil, fmt.Errorf("magnet: no urn:btih: found in xt parameters")
	}

	// Decode infohash: try hex (40 chars) or base32 (32 chars).
	infoHashStr = strings.ToLower(strings.TrimSpace(infoHashStr))
	if len(infoHashStr) == 40 {
		raw, err := hex.DecodeString(infoHashStr)
		if err != nil {
			return nil, fmt.Errorf("magnet: invalid hex infohash: %w", err)
		}
		m.InfoHash = infoHashStr
		m.InfoHashRaw = raw
	} else if len(infoHashStr) == 32 {
		raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(infoHashStr))
		if err != nil {
			// Try with padding
			raw, err = base32.StdEncoding.DecodeString(infoHashStr)
			if err != nil {
				return nil, fmt.Errorf("magnet: invalid base32 infohash: %w", err)
			}
		}
		m.InfoHash = hex.EncodeToString(raw)
		m.InfoHashRaw = raw
	} else {
		return nil, fmt.Errorf("magnet: infohash length %d (expected 40 hex or 32 base32)", len(infoHashStr))
	}

	// Extract dn (display name).
	if dns := values["dn"]; len(dns) > 0 {
		m.DisplayName = dns[0]
	}

	// Extract tr (tracker URLs).
	if trs := values["tr"]; len(trs) > 0 {
		m.Trackers = trs
	}

	log.LogInfo("bt-magnet: parsed infohash=%s name=%q trackers=%d",
		m.InfoHash, m.DisplayName, len(m.Trackers))
	return m, nil
}
