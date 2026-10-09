package signalling

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Discovery background: defensive test -- ID rules (alphanumeric at start/end) are
// a prerequisite for signaling registration success; rule regression protection.
// This test moved with validID -> ValidID into the signalling package.
func TestValidID(t *testing.T) {
	cases := []struct {
		id   string
		want bool
	}{
		{"peerdrive-abc123", true},
		{"a", true},
		{"a-b_c d", true}, // - _ and spaces allowed in the middle
		{"", false},
		{"-abc", false}, // first character must be alphanumeric
		{"abc-", false},
		{"a b!", false}, // illegal character
		{"ab\ncd", false},
		{strings.Repeat("a", 257), false}, // over 256 chars
	}
	for _, c := range cases {
		assert.Equal(t, c.want, ValidID(c.id), "id=%q", c.id)
	}
}

// TestRandomToken pins the token shape: 32 hex chars (16 bytes), never empty.
func TestRandomToken(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		tok := randomToken()
		require.Len(t, tok, 32)
		for _, c := range tok {
			require.True(t, (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f'), "non-hex %q in %q", c, tok)
		}
		seen[tok] = true
	}
	assert.Len(t, seen, 100, "tokens must be unique")
}

// TestNormalizeOptions pins the defaults and their precedence over caller values.
func TestNormalizeOptions(t *testing.T) {
	t.Run("zero value gets all defaults", func(t *testing.T) {
		o := NormalizeOptions(Options{})
		assert.Equal(t, "443", o.Port)
		assert.Equal(t, "/", o.Path)
		assert.Equal(t, "peerjs", o.Key)
		assert.Equal(t, int64(5)*1000*1000*1000, o.PingInterval.Nanoseconds())
		assert.Len(t, o.Token, 32, "an empty token must be generated")
	})
	t.Run("caller values are kept", func(t *testing.T) {
		want := "my-token"
		o := NormalizeOptions(Options{
			Host: "h", Port: "9000", Secure: true, Path: "/p",
			Key: "k", Token: want, PingInterval: 42,
		})
		assert.Equal(t, Options{
			Host: "h", Port: "9000", Secure: true, Path: "/p",
			Key: "k", Token: want, PingInterval: 42,
		}, o)
	})
	t.Run("idempotent", func(t *testing.T) {
		o := NormalizeOptions(Options{})
		assert.Equal(t, o, NormalizeOptions(o))
	})
	t.Run("default options", func(t *testing.T) {
		d := DefaultOptions()
		assert.Equal(t, "peersignal.moonchan.xyz", d.Host)
		assert.Equal(t, "443", d.Port)
		assert.True(t, d.Secure)
		assert.Equal(t, "/", d.Path)
		assert.Equal(t, "pd-signal-1edf5e05e4a52b7351392574", d.Key)
		assert.Equal(t, int64(5)*1000*1000*1000, d.PingInterval.Nanoseconds())
		assert.Empty(t, d.Token)
	})
}
