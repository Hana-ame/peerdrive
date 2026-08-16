package p2p_bt

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"


	dht "github.com/anacrolix/dht/v2"
	"github.com/anacrolix/dht/v2/krpc"
)

// BEP 51 (DHT Infohash Indexing) allows querying DHT nodes for a sample of
// infohashes they know about.

var (
	// ErrBEP51DHTDisabled is returned when the DHT server is not available.
	ErrBEP51DHTDisabled = errors.New("DHT server not available")
	// ErrBEP51NoSamples is returned when none of the queried nodes returned
	// samples.
	ErrBEP51NoSamples = errors.New("no infohash samples returned from DHT")
)

// SampleInfohashes 查询 DHT 节点获取其已知的 infohash 样本。
func (s *BTDHTService) SampleInfohashes(target [20]byte) (samples [][20]byte, err error) {
	if s.Server == nil {
		return nil, ErrBEP51DHTDisabled
	}
	defer LogDuration("BTDHT.SampleInfohashes")()
	LogDebug("bt-dht: SampleInfohashes target=%x", target)

	// Get a set of nodes from our routing table to query.
	nodes := s.closestNodes(target, 8)
	if len(nodes) == 0 {
		return nil, fmt.Errorf("no DHT nodes to query")
	}

	type sampleResult struct {
		samples [][20]byte
		err     error
	}
	resultCh := make(chan sampleResult, len(nodes))

	// Query multiple nodes in parallel.
	for _, ni := range nodes {
		ni := ni
		go func() {
			addr := dht.NewAddr(&net.UDPAddr{IP: ni.Addr.IP, Port: ni.Addr.Port})
			samples, err := s.queryNodeForSamples(addr, target)
			if err != nil {
				LogDebug("bt-dht: SampleInfohashes query to %s failed: %v",
					ni.Addr.String(), err)
				resultCh <- sampleResult{err: err}
				return
			}
			resultCh <- sampleResult{samples: samples}
		}()
	}

	var allSamples [][20]byte
	seen := make(map[[20]byte]bool)
	var firstErr error

	for i := 0; i < len(nodes); i++ {
		rr := <-resultCh
		if rr.err != nil {
			if firstErr == nil {
				firstErr = rr.err
			}
			continue
		}
		for _, s := range rr.samples {
			if !seen[s] {
				seen[s] = true
				allSamples = append(allSamples, s)
			}
		}
	}

	if len(allSamples) == 0 {
		// Our local server may have samples from its routing table too.
		localSamples, localErr := s.queryServerForSamples(target)
		if localErr == nil {
			for _, s := range localSamples {
				if !seen[s] {
					seen[s] = true
					allSamples = append(allSamples, s)
				}
			}
		}
	}

	if len(allSamples) == 0 {
		if firstErr != nil {
			return nil, fmt.Errorf("%w: %v", ErrBEP51NoSamples, firstErr)
		}
		return nil, ErrBEP51NoSamples
	}

	LogInfo("bt-dht: SampleInfohashes collected %d unique samples from %d nodes",
		len(allSamples), len(nodes))
	return allSamples, nil
}

// queryNodeForSamples sends a sample_infohashes query to a single node.
func (s *BTDHTService) queryNodeForSamples(addr dht.Addr, target [20]byte) ([][20]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	qi := dht.QueryInput{
		MsgArgs: krpc.MsgArgs{
			ID:     s.Server.ID(),
			Target: target,
		},
		RateLimiting: dht.QueryRateLimiting{NotFirst: true},
	}

	res := s.Server.Query(ctx, addr, "sample_infohashes", qi)
	if res.ToError() != nil {
		return nil, res.Err
	}
	if res.Reply.R == nil {
		return nil, errors.New("empty response")
	}

	r := res.Reply.R
	if r.Samples != nil {
		return *r.Samples, nil
	}
	return nil, nil
}

// queryServerForSamples tries to get infohash samples from our own server.
// The dht.Server does not natively support sample_infohashes queries, but
// we can attempt the query locally.
func (s *BTDHTService) queryServerForSamples(target [20]byte) ([][20]byte, error) {
	localAddr := dht.NewAddr(s.Server.Addr())
	return s.queryNodeForSamples(localAddr, target)
}

// DiscoverInfohashes 爬取 DHT 路由表，收集附近节点已知的 infohashes。
func (s *BTDHTService) DiscoverInfohashes(maxResults int) ([][20]byte, error) {
	if s.Server == nil {
		return nil, ErrBEP51DHTDisabled
	}
	defer LogDuration("BTDHT.DiscoverInfohashes")()
	LogDebug("bt-dht: DiscoverInfohashes max=%d", maxResults)

	return s.crawlInfohashes(maxResults)
}

// crawlInfohashes iteratively samples infohashes from DHT nodes until
// maxResults unique results are collected or we exhaust available nodes.
func (s *BTDHTService) crawlInfohashes(maxResults int) ([][20]byte, error) {
	// Start with nodes closest to our own node ID.
	var selfID [20]byte
	serverID := s.Server.ID()
	copy(selfID[:], serverID[:])

	// Generate a few random targets to sample diverse buckets.
	targets := make([][20]byte, 3)
	for i := range targets {
		rand.Read(targets[i][:])
	}

	seen := make(map[[20]byte]bool)
	var results [][20]byte
	var mu sync.Mutex

	// Helper to check if we reached the maximum.
	done := func() bool {
		mu.Lock()
		defer mu.Unlock()
		return maxResults > 0 && len(results) >= maxResults
	}

	// Query nodes using multiple random targets to cover different buckets.
	var wg sync.WaitGroup
	for _, tgt := range targets {
		if done() {
			break
		}
		tgt := tgt
		nodes := s.closestNodes(tgt, 8)
		if len(nodes) == 0 {
			continue
		}
		for _, ni := range nodes {
			if done() {
				break
			}
			ni := ni
			wg.Add(1)
			go func() {
				defer wg.Done()
				addr := dht.NewAddr(&net.UDPAddr{IP: ni.Addr.IP, Port: ni.Addr.Port})
				samples, err := s.queryNodeForSamples(addr, tgt)
				if err != nil {
					return
				}
				mu.Lock()
				defer mu.Unlock()
				for _, ih := range samples {
					if !seen[ih] {
						seen[ih] = true
						results = append(results, ih)
						if maxResults > 0 && len(results) >= maxResults {
							return
						}
					}
				}
			}()
		}
	}
	wg.Wait()

	if len(results) == 0 {
		return nil, ErrBEP51NoSamples
	}

	if maxResults > 0 && len(results) > maxResults {
		results = results[:maxResults]
	}

	LogInfo("bt-dht: DiscoverInfohashes collected %d unique infohashes",
		len(results))
	return results, nil
}
