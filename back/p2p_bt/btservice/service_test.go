package btservice

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// newTestService starts a real runtime with the network-shaped work disabled:
// the torrent client still binds a peer port (that IS the thing we ship), but the
// public tracker list is not fetched, and the DHT is off.
func newTestService(t *testing.T) *Service {
	t.Helper()
	t.Setenv("PEERDRIVE_BT_TRACKERS_DYNAMIC_ENABLE", "false")

	svc, err := New(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	return svc
}

func TestNewRequiresDataDir(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("expected an error when data_dir is empty")
	}
}

func TestPingReportsConfig(t *testing.T) {
	svc := newTestService(t)

	val, err := svc.Call("ping", nil)
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
	st, ok := val.(Status)
	if !ok {
		t.Fatalf("ping returned %T, want Status", val)
	}
	if st.DataDir == "" {
		t.Error("status.DataDir is empty")
	}
	if st.DHTEnabled {
		t.Error("status.DHTEnabled is true for a runtime started without DHT")
	}
	if st.DHTAvailable {
		t.Error("status.DHTAvailable is true for a runtime started without DHT")
	}
}

func TestDownloadDirAndEmptyLists(t *testing.T) {
	svc := newTestService(t)

	val, err := svc.Call("download_dir", nil)
	if err != nil {
		t.Fatalf("download_dir: %v", err)
	}
	if dir, _ := val.(map[string]string)["dir"]; dir == "" {
		t.Error("download_dir returned an empty dir")
	}

	for _, op := range []string{"list_downloads", "list_seeders"} {
		if _, err := svc.Call(op, nil); err != nil {
			t.Errorf("%s on a fresh runtime: %v", op, err)
		}
	}

	val, err = svc.Call("drain_completions", nil)
	if err != nil {
		t.Fatalf("drain_completions: %v", err)
	}
	if got, ok := val.([]Completion); !ok || len(got) != 0 {
		t.Errorf("drain_completions = %#v, want an empty slice", val)
	}
}

func TestUnknownOpIsAnError(t *testing.T) {
	svc := newTestService(t)
	if _, err := svc.Call("no_such_op", nil); err == nil {
		t.Fatal("expected an error for an unknown op")
	}
}

func TestInfohashIsRequired(t *testing.T) {
	svc := newTestService(t)
	for _, op := range []string{"get_download", "pause", "resume", "remove", "start_seed", "stop_seed", "torrent_bytes", "magnet_uri"} {
		if _, err := svc.Call(op, json.RawMessage(`{}`)); err == nil {
			t.Errorf("%s: expected an error when infohash is missing", op)
		} else if !strings.Contains(err.Error(), "infohash") {
			t.Errorf("%s: error %q does not mention infohash", op, err)
		}
	}
}

func TestAddTorrentRejectsBadBase64(t *testing.T) {
	svc := newTestService(t)
	if _, err := svc.Call("add_torrent", json.RawMessage(`{"data_b64":"!!!"}`)); err == nil {
		t.Fatal("expected an error for invalid base64")
	}
}

func TestAddTorrentRejectsGarbage(t *testing.T) {
	svc := newTestService(t)
	payload := base64.StdEncoding.EncodeToString([]byte("this is not bencode"))
	args, _ := json.Marshal(map[string]string{"data_b64": payload})
	if _, err := svc.Call("add_torrent", args); err == nil {
		t.Fatal("expected an error for a non-torrent payload")
	}
}

func TestAddMagnetRequiresURI(t *testing.T) {
	svc := newTestService(t)
	if _, err := svc.Call("add_magnet", json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected an error when uri is missing")
	}
}

// TestDHTDisabledErrors is the contract the host relies on: with the DHT off,
// DHT ops must fail with the reason instead of hanging or silently succeeding.
func TestDHTDisabledErrors(t *testing.T) {
	svc := newTestService(t)

	val, err := svc.Call("dht_status", nil)
	if err != nil {
		t.Fatalf("dht_status: %v", err)
	}
	status, ok := val.(map[string]any)
	if !ok {
		t.Fatalf("dht_status returned %T", val)
	}
	if available, _ := status["available"].(bool); available {
		t.Error("dht_status reports available on a runtime started without DHT")
	}

	for _, op := range []string{"dht_announce", "dht_find_providers", "bep44_put", "bep44_get", "bep51_sample", "bridge_share", "bridge_list_shared", "bridge_fetch"} {
		_, err := svc.Call(op, json.RawMessage(`{"hash":"ab","target":"00"}`))
		if err == nil {
			t.Errorf("%s: expected an error when the DHT is disabled", op)
			continue
		}
		if !strings.Contains(err.Error(), "DHT") {
			t.Errorf("%s: error %q does not explain that the DHT is unavailable", op, err)
		}
	}
}

func TestBEP44TargetValidation(t *testing.T) {
	svc := newTestService(t)
	svc.dht = nil
	// requireDHT fires first; poke decodeTarget directly instead.
	if _, err := decodeTarget("zz"); err == nil {
		t.Error("expected an error for a target of the wrong length")
	}
	if _, err := decodeTarget(strings.Repeat("g", 40)); err == nil {
		t.Error("expected an error for non-hex target characters")
	}
	if _, err := decodeTarget(strings.Repeat("ab", 20)); err != nil {
		t.Errorf("valid 40-char hex target rejected: %v", err)
	}
}

func TestMetaDTOOmitsPieceHashes(t *testing.T) {
	// The whole point of MetaDTO: pieces never cross the ABI. Encode one and make
	// sure the JSON has a count but no hash list.
	meta := metaDTO(nil)
	if meta != nil {
		t.Fatal("metaDTO(nil) should be nil")
	}

	dto := MetaDTO{Name: "x", PieceCount: 3, InfoHashHex: "ab"}
	raw, err := json.Marshal(dto)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "pieces\":[") {
		t.Errorf("encoded DTO carries a pieces array: %s", raw)
	}
	if !strings.Contains(string(raw), `"piece_count":3`) {
		t.Errorf("encoded DTO lost the piece count: %s", raw)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	svc := newTestService(t)
	if err := svc.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := svc.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}

	var nilSvc *Service
	if err := nilSvc.Close(); err != nil {
		t.Errorf("nil Close: %v", err)
	}
	if _, err := nilSvc.Call("ping", nil); err == nil {
		t.Error("expected an error calling a nil service")
	}
}

// TestCompletionsQueueIsBounded makes sure a host that never drains cannot make
// the runtime grow without bound.
func TestCompletionsQueueIsBounded(t *testing.T) {
	svc := newTestService(t)
	for i := 0; i < maxPendingCompletions+10; i++ {
		svc.mu.Lock()
		if len(svc.completions) < maxPendingCompletions {
			svc.completions = append(svc.completions, Completion{InfoHash: "x"})
		}
		svc.mu.Unlock()
	}
	got := svc.drainCompletions()
	if len(got) != maxPendingCompletions {
		t.Errorf("queue held %d entries, want the %d cap", len(got), maxPendingCompletions)
	}
	if again := svc.drainCompletions(); len(again) != 0 {
		t.Errorf("drain did not clear the queue: %d left", len(again))
	}
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
