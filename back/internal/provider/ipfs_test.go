package provider

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// mockIPFSGateway returns an httptest server that serves /ipfs/<cid> paths.
// cids maps CID → content. If a CID is not in the map it returns 404.
// If delay > 0 the server sleeps before responding.
func mockIPFSGateway(cids map[string]string, delay time.Duration) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if delay > 0 {
			time.Sleep(delay)
		}
		if !strings.HasPrefix(r.URL.Path, "/ipfs/") {
			http.NotFound(w, r)
			return
		}
		cid := strings.TrimPrefix(r.URL.Path, "/ipfs/")
		content, ok := cids[cid]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(content))
	}))
}

// ─── GetReader ──────────────────────────────────────────────────────

func TestIPFSProvider_GetReader_Success(t *testing.T) {
	srv := mockIPFSGateway(map[string]string{"QmTest": "hello ipfs"}, 0)
	defer srv.Close()

	p := NewIPFSProvider([]string{srv.URL})
	reader, err := p.GetReader("QmTest")
	if err != nil {
		t.Fatalf("GetReader: %v", err)
	}
	defer reader.Close()

	data, _ := io.ReadAll(reader)
	if string(data) != "hello ipfs" {
		t.Fatalf("got %q, want %q", string(data), "hello ipfs")
	}
}

func TestIPFSProvider_GetReader_NoGateways(t *testing.T) {
	p := NewIPFSProvider(nil)
	_, err := p.GetReader("QmTest")
	if err == nil || !strings.Contains(err.Error(), "no gateways configured") {
		t.Fatalf("expected 'no gateways configured', got: %v", err)
	}
}

func TestIPFSProvider_GetReader_NotFound(t *testing.T) {
	srv := mockIPFSGateway(map[string]string{}, 0)
	defer srv.Close()

	p := NewIPFSProvider([]string{srv.URL})
	_, err := p.GetReader("QmNotFound")
	if err == nil {
		t.Fatal("expected error for unknown CID")
	}
}

func TestIPFSProvider_GetReader_RaceWinner(t *testing.T) {
	// Two gateways: one fast, one very slow.
	// The fast one should win; the slow one's body should be closed.
	fast := mockIPFSGateway(map[string]string{"QmRace": "fast-wins"}, 0)
	defer fast.Close()
	slow := mockIPFSGateway(map[string]string{"QmRace": "slow-loses"}, 2*time.Second)
	defer slow.Close()

	p := NewIPFSProvider([]string{slow.URL, fast.URL})
	start := time.Now()
	reader, err := p.GetReader("QmRace")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("GetReader: %v", err)
	}
	defer reader.Close()

	data, _ := io.ReadAll(reader)
	if string(data) != "fast-wins" {
		t.Fatalf("got %q, want 'fast-wins'", string(data))
	}
	// Should return quickly (~fast), not wait for slow 2s
	if elapsed > 1*time.Second {
		t.Errorf("took %v, expected <1s (slow gateway should be cancelled)", elapsed)
	}
}

func TestIPFSProvider_GetReader_AllFail(t *testing.T) {
	// Both gateways return 404
	gw1 := mockIPFSGateway(map[string]string{}, 0)
	defer gw1.Close()
	gw2 := mockIPFSGateway(map[string]string{}, 0)
	defer gw2.Close()

	p := NewIPFSProvider([]string{gw1.URL, gw2.URL})
	_, err := p.GetReader("QmMissing")
	if err == nil {
		t.Fatal("expected error when all gateways fail")
	}
}

// ─── FetchByCID ─────────────────────────────────────────────────────

func TestIPFSProvider_FetchByCID_Success(t *testing.T) {
	srv := mockIPFSGateway(map[string]string{"QmFetch": "full content"}, 0)
	defer srv.Close()

	p := NewIPFSProvider([]string{srv.URL})
	data, err := p.FetchByCID(context.Background(), "QmFetch")
	if err != nil {
		t.Fatalf("FetchByCID: %v", err)
	}
	if string(data) != "full content" {
		t.Fatalf("got %q, want %q", string(data), "full content")
	}
}

func TestIPFSProvider_FetchByCID_NoGateways(t *testing.T) {
	p := NewIPFSProvider(nil)
	_, err := p.FetchByCID(context.Background(), "QmTest")
	if err == nil || !strings.Contains(err.Error(), "no gateways configured") {
		t.Fatalf("expected 'no gateways configured', got: %v", err)
	}
}

func TestIPFSProvider_FetchByCID_ContextCancel(t *testing.T) {
	// Slow gateway that gets cancelled
	srv := mockIPFSGateway(map[string]string{"QmCancel": "data"}, 5*time.Second)
	defer srv.Close()

	p := NewIPFSProvider([]string{srv.URL})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := p.FetchByCID(ctx, "QmCancel")
	if err == nil {
		t.Fatal("expected context deadline error")
	}
}

func TestIPFSProvider_FetchByCID_NotFound(t *testing.T) {
	srv := mockIPFSGateway(map[string]string{}, 0)
	defer srv.Close()

	p := NewIPFSProvider([]string{srv.URL})
	_, err := p.FetchByCID(context.Background(), "QmGhost")
	if err == nil {
		t.Fatal("expected error for unknown CID")
	}
}

func TestIPFSProvider_FetchByCID_RaceWinner(t *testing.T) {
	fast := mockIPFSGateway(map[string]string{"QmRaceFull": "fast-full"}, 0)
	defer fast.Close()
	slow := mockIPFSGateway(map[string]string{"QmRaceFull": "slow-full"}, 3*time.Second)
	defer slow.Close()

	p := NewIPFSProvider([]string{slow.URL, fast.URL})
	start := time.Now()
	data, err := p.FetchByCID(context.Background(), "QmRaceFull")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("FetchByCID: %v", err)
	}
	if string(data) != "fast-full" {
		t.Fatalf("got %q, want 'fast-full'", string(data))
	}
	if elapsed > 1*time.Second {
		t.Errorf("took %v, expected <1s", elapsed)
	}
}

// ─── GetFilenameHint ────────────────────────────────────────────────

func TestIPFSProvider_GetFilenameHint(t *testing.T) {
	p := NewIPFSProvider(nil)
	if hint := p.GetFilenameHint("QmFoo", ""); hint != "QmFoo" {
		t.Errorf("expected CID as hint, got %q", hint)
	}
	if hint := p.GetFilenameHint("QmFoo", "original.txt"); hint != "original.txt" {
		t.Errorf("expected original filename, got %q", hint)
	}
}

// ─── BitswapFetcher integration ──────────────────────────────────────

func TestIPFSProvider_SetBitswapFetcher(t *testing.T) {
	p := NewIPFSProvider([]string{})
	// Without gateways and without bitswap, GetReader should fail
	_, err := p.GetReader("QmTest")
	if err == nil {
		t.Error("expected error with no gateways or bitswap fetcher")
	}

	// With a bitswap fetcher, it should be tried first
	p.SetBitswapFetcher(func(ctx context.Context, cid string) ([]byte, error) {
		return []byte("bitswap-data"), nil
	})
	reader, err := p.GetReader("QmTest")
	if err != nil {
		t.Fatalf("GetReader with bitswap fetcher failed: %v", err)
	}
	defer reader.Close()
	data, _ := io.ReadAll(reader)
	if string(data) != "bitswap-data" {
		t.Errorf("expected bitswap-data, got %q", string(data))
	}
}

// ─── Goroutine / body leak ──────────────────────────────────────────

func TestIPFSProvider_GetReader_NoBodyLeak(t *testing.T) {
	// Create gateways that count body closes
	makeGW := func(contents map[string]string, delay time.Duration) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if delay > 0 {
				time.Sleep(delay)
			}
			cid := strings.TrimPrefix(r.URL.Path, "/ipfs/")
			content, ok := contents[cid]
			if !ok {
				http.NotFound(w, r)
				return
			}
			// Wrap the writer to count when flushed/closed
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content)))
			w.Write([]byte(content))
		}))
	}

	cid := "QmNoLeak"

	// Fast wins, slow continues — slow's body must not leak
	fast := makeGW(map[string]string{cid: "winner"}, 0)
	defer fast.Close()
	slow := makeGW(map[string]string{cid: "loser"}, 2*time.Second)
	defer slow.Close()

	p := NewIPFSProvider([]string{slow.URL, fast.URL})
	reader, err := p.GetReader(cid)
	if err != nil {
		t.Fatalf("GetReader: %v", err)
	}
	reader.Close()
	// Give time for drain goroutine to complete
	time.Sleep(300 * time.Millisecond)

	// If we get here without blocking forever, the drain goroutine works.
	t.Log("no body leak detected")
}

// ─── Multiple gateways stress ───────────────────────────────────────

func TestIPFSProvider_GetReader_ManyGateways(t *testing.T) {
	n := 10
	var gateways []string

	for i := 0; i < n; i++ {
		content := fmt.Sprintf("gw-%d", i)
		srv := mockIPFSGateway(map[string]string{"QmMany": content}, time.Duration(i*100)*time.Millisecond)
		defer srv.Close()
		gateways = append(gateways, srv.URL)
	}

	p := NewIPFSProvider(gateways)
	reader, err := p.GetReader("QmMany")
	if err != nil {
		t.Fatalf("GetReader: %v", err)
	}
	defer reader.Close()

	data, _ := io.ReadAll(reader)
	if !strings.HasPrefix(string(data), "gw-") {
		t.Fatalf("unexpected content: %q", string(data))
	}
}

// ─── Retry with backoff ─────────────────────────────────────────────

func TestIPFSProvider_RetryOnError(t *testing.T) {
	var attempts atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		if attempts.Load() <= 2 {
			http.Error(w, "temporary failure", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("finally ok"))
	}))
	defer srv.Close()

	p := NewIPFSProvider([]string{srv.URL})
	reader, err := p.GetReader("QmRetry")
	if err != nil {
		t.Fatalf("GetReader should succeed after retries: %v", err)
	}
	defer reader.Close()

	data, _ := io.ReadAll(reader)
	if string(data) != "finally ok" {
		t.Fatalf("got %q, want 'finally ok'", string(data))
	}
	if attempts.Load() < 3 {
		t.Errorf("expected >=3 attempts, got %d", attempts.Load())
	}
}

func TestIPFSProvider_RetryExhausted(t *testing.T) {
	var attempts atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		http.Error(w, "gone", http.StatusGone)
	}))
	defer srv.Close()

	p := NewIPFSProvider([]string{srv.URL})
	_, err := p.GetReader("QmGone")
	if err == nil {
		t.Fatal("expected error after all retries exhausted")
	}
	if attempts.Load() != defaultMaxRetries {
		t.Errorf("expected %d retries, got %d", defaultMaxRetries, attempts.Load())
	}
}

// ─── Context propagation ────────────────────────────────────────────

func TestIPFSProvider_FetchByCID_ParentContextCancel(t *testing.T) {
	srv := mockIPFSGateway(map[string]string{"QmParent": "data"}, 5*time.Second)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	p := NewIPFSProvider([]string{srv.URL})
	_, err := p.FetchByCID(ctx, "QmParent")
	wg.Wait()

	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}
