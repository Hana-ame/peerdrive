package signalling

import (
	"crypto/rand"
	"encoding/hex"
)

// version mimics peerjs-client's version query parameter. It is part of the
// PeerJS wire handshake (the ?version= query on both the retrieve-id and the
// websocket URL), so pinning it is a protocol requirement, not cosmetics.
const version = "1.5.4"

// ValidID validates PeerJS ID rules: first and last characters must be alphanumeric; the middle may contain - _ space.
func ValidID(id string) bool {
	if len(id) < 1 || len(id) > 256 {
		return false
	}
	isAlnum := func(c byte) bool {
		return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
	}
	if !isAlnum(id[0]) || !isAlnum(id[len(id)-1]) {
		return false
	}
	for i := 1; i < len(id)-1; i++ {
		c := id[i]
		if !isAlnum(c) && c != '-' && c != '_' && c != ' ' {
			return false
		}
	}
	return true
}

// randomToken generates a random token (alphanumeric, mimicking peerjs util.randomToken).
func randomToken() string {
	return randHex(16)
}

// randHex generates n bytes of random hex.
func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
