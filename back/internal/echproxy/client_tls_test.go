package echproxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// entryTLSListener stands in for the local ech-proxy entry: a loopback TLS listener
// whose certificate is NOT in the system trust store (httptest's test CA), exactly the
// condition the entry host has in production (self-signed *.l.moonchan.xyz).
func entryTLSListener(t *testing.T) string {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().String()
}

// The ":<port>" in this URL is a red herring by design: DialTLSContext rewrites the TCP
// target to the configured listen address and keeps the URL host only as the SNI, which
// is how the entry host is recognised.
const entryURL = "https://iwara-api." + DefaultEntrySuffix + ":" + DefaultEntryPort + "/video/x"

// Default behaviour is unchanged: the module config default accepts the self-signed
// local proxy, so no operator action is needed for existing deployments.
func TestIwaraClient_EntrySkipTLSDefaultAllowsSelfSignedEntry(t *testing.T) {
	cfg := NewModuleConfig()
	cfg.ListenAddr = entryTLSListener(t)
	client := NewIwaraClient(&cfg)

	resp, err := client.Do(context.Background(), http.MethodGet, entryURL, nil)
	require.NoError(t, err, "default EntrySkipTLS=true must accept the self-signed local proxy")
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// The opt-in hardening path: with verification enforced, the same self-signed entry is
// rejected instead of silently trusted. This is the behaviour a deployment that owns a
// chain-issued entry cert relies on.
func TestIwaraClient_EntrySkipTLSFalseEnforcesChainVerification(t *testing.T) {
	cfg := NewModuleConfig()
	cfg.ListenAddr = entryTLSListener(t)
	cfg.EntrySkipTLS = false
	client := NewIwaraClient(&cfg)

	_, err := client.Do(context.Background(), http.MethodGet, entryURL, nil)
	require.Error(t, err, "verification on must reject the untrusted local certificate")
	assert.Contains(t, err.Error(), "x509", "the failure must be a certificate verification failure, not a network error")
}

// The documented default must stay true — flipping it would break every existing
// iwara/ech-proxy deployment (G402 triage: option B keeps zero behaviour change).
func TestNewModuleConfig_EntrySkipTLSDefaultIsTrue(t *testing.T) {
	cfg := NewModuleConfig()
	cfg.Normalize()
	assert.True(t, cfg.EntrySkipTLS, "PEERDRIVE_IWARA_ENTRY_SKIP_TLS default must remain true")
}
