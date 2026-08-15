// Package p2p_bt 实现 BitTorrent DHT 的 BEP 44 协议，支持不可变和可变数据的存取。
package p2p_bt

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"net"
	"sort"
	"sync"
	"time"

	"peerdrive/internal/log"

	dht "github.com/anacrolix/dht/v2"
	"github.com/anacrolix/dht/v2/bep44"
	"github.com/anacrolix/dht/v2/int160"
	"github.com/anacrolix/dht/v2/krpc"
	"github.com/anacrolix/torrent/bencode"
)

var (
	// ErrBEP44NotFound is returned when a BEP 44 Get query yields no result.
	ErrBEP44NotFound = errors.New("BEP 44 item not found in DHT")
	// ErrBEP44DHTDisabled is returned when the DHT server is not available.
	ErrBEP44DHTDisabled = errors.New("DHT server not available")
)

// ----- Immutable items -----

// PutImmutable 将不可变数据存入 DHT（最多 1000 字节），返回 20 字节的 infohash 目标值。
func (s *BTDHTService) PutImmutable(data []byte) (target [20]byte, err error) {
	if s.Server == nil {
		return target, ErrBEP44DHTDisabled
	}
	defer log.LogDuration("BTDHT.PutImmutable")()
	log.LogDebug("bt-dht: PutImmutable data=%d bytes", len(data))

	// Wrap raw bytes as a bencode byte string.
	put := bep44.Put{V: bencode.Bytes(data)}

	// Validate size: the bencoded v field must be <= 1000 bytes.
	bv, _ := bencode.Marshal(put.V)
	if len(bv) > 1000 {
		return target, fmt.Errorf("value too large: %d bytes (max 1000)", len(bv))
	}
	target = put.Target()

	// 1. Store in our local in-memory store for reliable retrieval by GetImmutable.
	s.localBEP44Store.Store(target, data)

	// 2. Store locally using our own server (so we can answer get queries from other nodes).
	s.putLocal(put, target)

	// 3. Best-effort: store on remote close nodes. Do NOT fail if this doesn't
	//    work — most DHT nodes do not support BEP 44 arbitrary data storage.
	nodes := s.closestNodes(target, 8)
	if len(nodes) == 0 {
		log.LogDebug("bt-dht: PutImmutable no close nodes for remote storage")
	} else {
		var success bool
		for _, ni := range nodes {
			addr := dht.NewAddr(&net.UDPAddr{IP: ni.Addr.IP, Port: ni.Addr.Port})

			// BEP 44 requires a write token obtained from a prior get.
			getRes := s.Server.Get(context.Background(), addr, target, nil,
				dht.QueryRateLimiting{NotFirst: true})
			if getRes.ToError() != nil {
				continue
			}
			if getRes.Reply.R == nil || getRes.Reply.R.Token == nil {
				continue
			}
			token := *getRes.Reply.R.Token

			putRes := s.Server.Put(context.Background(), addr, put, token,
				dht.QueryRateLimiting{NotFirst: true})
			if putRes.ToError() != nil {
				continue
			}
			success = true
			log.LogDebug("bt-dht: PutImmutable stored on %s", ni.Addr.String())
			break
		}
		if !success {
			log.LogWarn("bt-dht: PutImmutable remote storage all failed (local copy saved)")
		}
	}

	log.LogInfo("bt-dht: PutImmutable target=%x size=%d", target, len(data))
	return target, nil
}

// GetImmutable 根据 20 字节 infohash 从 DHT 检索不可变数据。
// 优先检查本地缓存（此前 PutImmutable 存入），再回退到 DHT 网络查找。
func (s *BTDHTService) GetImmutable(target [20]byte) (data []byte, err error) {
	if s.Server == nil {
		return nil, ErrBEP44DHTDisabled
	}
	defer log.LogDuration("BTDHT.GetImmutable")()
	log.LogDebug("bt-dht: GetImmutable target=%x", target)

	// Check local store first — guarantees roundtrip for data we put ourselves.
	if val, ok := s.localBEP44Store.Load(target); ok {
		log.LogDebug("bt-dht: GetImmutable found in local store target=%x", target)
		return val.([]byte), nil
	}

	msg, err := s.lookupValue(target, nil)
	if err != nil {
		return nil, err
	}

	return decodeBEP44Value(msg.R.V)
}

// ----- Mutable items -----

// PutMutable 将可变数据存入 DHT，使用 Ed25519 私钥签名，seq 序号需随每次更新递增。
func (s *BTDHTService) PutMutable(
	privKey ed25519.PrivateKey,
	salt []byte,
	data []byte,
	seq uint64,
) (target [20]byte, err error) {
	if s.Server == nil {
		return target, ErrBEP44DHTDisabled
	}
	defer log.LogDuration("BTDHT.PutMutable")()
	log.LogDebug("bt-dht: PutMutable seq=%d salt=%x data=%d bytes", seq, salt, len(data))

	// Build the mutable put.
	v := bencode.Bytes(data)
	put := bep44.Put{
		V:    v,
		Salt: salt,
		Seq:  int64(seq),
	}
	// Set public key.
	pk := [32]byte{}
	pubKeyBytes := []byte(privKey.Public().(ed25519.PublicKey))
	copy(pk[:], pubKeyBytes)
	put.K = &pk
	// Sign.
	put.Sign(privKey)

	// Validate.
	bv, _ := bencode.Marshal(put.V)
	if len(bv) > 1000 {
		return target, fmt.Errorf("value too large: %d bytes (max 1000)", len(bv))
	}
	if len(put.Salt) > 64 {
		return target, fmt.Errorf("salt too large: %d bytes (max 64)", len(put.Salt))
	}

	target = put.Target()

	// Store locally.
	s.putLocal(put, target)

	// Store on remote close nodes.
	nodes := s.closestNodes(target, 8)
	if len(nodes) == 0 {
		log.LogWarn("bt-dht: PutMutable no close nodes found")
		return target, nil
	}

	var lastErr error
	var success bool
	for _, ni := range nodes {
		addr := dht.NewAddr(&net.UDPAddr{IP: ni.Addr.IP, Port: ni.Addr.Port})
		getRes := s.Server.Get(context.Background(), addr, target, nil,
			dht.QueryRateLimiting{NotFirst: true})
		if getRes.ToError() != nil {
			continue
		}
		if getRes.Reply.R == nil || getRes.Reply.R.Token == nil {
			continue
		}
		token := *getRes.Reply.R.Token

		putRes := s.Server.Put(context.Background(), addr, put, token,
			dht.QueryRateLimiting{NotFirst: true})
		if putRes.ToError() != nil {
			lastErr = putRes.Err
			continue
		}
		success = true
		lastErr = nil
		log.LogDebug("bt-dht: PutMutable stored on %s", ni.Addr.String())
		break
	}
	if !success && lastErr != nil {
		return target, fmt.Errorf("put to all nodes failed: %w", lastErr)
	}

	log.LogInfo("bt-dht: PutMutable target=%x seq=%d stored on %d nodes",
		target, seq, len(nodes))
	return target, nil
}

// GetMutable 从 DHT 检索可变数据，返回原始数据字节和响应节点的序列号。
func (s *BTDHTService) GetMutable(
	pubKey ed25519.PublicKey,
	salt []byte,
) (data []byte, seq uint64, err error) {
	if s.Server == nil {
		return nil, 0, ErrBEP44DHTDisabled
	}
	defer log.LogDuration("BTDHT.GetMutable")()
	log.LogDebug("bt-dht: GetMutable pubkey=%x salt=%x", pubKey, salt)

	// Build target: SHA1(pubkey || salt).
	var pk [32]byte
	copy(pk[:], pubKey)
	target := bep44.MakeMutableTarget(pk, salt)

	seqFilter := int64(-1) // request any sequence
	msg, err := s.lookupValue(target, &seqFilter)
	if err != nil {
		return nil, 0, err
	}

	r := msg.R
	raw, err := decodeBEP44Value(r.V)
	if err != nil {
		return nil, 0, err
	}
	if r.Seq != nil {
		seq = uint64(*r.Seq)
	}
	return raw, seq, nil
}

// ----- internal helpers -----

// putLocal stores the item in the local DHT server so we can serve it to
// other nodes.
//
// dht.Server.Put() stores the item in its internal store (line 1081 of
// server.go) BEFORE sending any network query. We pass a cancelled context so
// the local store is updated but the network query returns immediately.
func (s *BTDHTService) putLocal(put bep44.Put, target [20]byte) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // immediately cancelled — local store is updated before Query runs
	dummyAddr := dht.NewAddr(&net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1})
	putRes := s.Server.Put(ctx, dummyAddr, put, "", dht.QueryRateLimiting{})
	if putRes.ToError() != nil {
		// Expected: the network query was cancelled. The local store was
		// updated successfully before the cancellation was checked.
		log.LogDebug("bt-dht: putLocal result (expected-cancel): %v", putRes.ToError())
	}
}

// lookupValue performs an iterative Kademlia lookup for the target using
// "get" queries. It returns the first KRPC message whose response includes
// a non-empty v field. If seq is non-nil, it is passed as the seq filter.
func (s *BTDHTService) lookupValue(target [20]byte, seq *int64) (*krpc.Msg, error) {
	type candidate struct {
		ni   krpc.NodeInfo
		dist int160.T
	}

	// Derive the int160 representation of the target.
	targetInt160 := int160.FromByteArray(target)

	// Collect initial candidates from the routing table.
	startNodes, err := s.Server.TraversalStartingNodes()
	if err != nil {
		log.LogDebug("bt-dht: no starting nodes from routing table: %v", err)
		startNodes = nil
	}

	candidates := make([]candidate, 0, len(startNodes))
	for _, n := range startNodes {
		if !n.Id.Ok {
			continue
		}
		id := n.Id.Value
		ni := krpc.NodeInfo{
			ID:   id.AsByteArray(),
			Addr: n.Addr.ToNodeAddr(),
		}
		dist := id.Distance(targetInt160)
		candidates = append(candidates, candidate{ni: ni, dist: dist})
	}

	queried := make(map[string]bool) // "ip:port" -> queried
	var mu sync.Mutex

	// Up to 8 rounds of iterative lookup.
	for round := 0; round < 8; round++ {
		// Sort candidates by distance to target.
		sort.Slice(candidates, func(i, j int) bool {
			return candidates[i].dist.Cmp(candidates[j].dist) < 0
		})

		// Pick up to alpha unqueried closest candidates.
		alpha := 3
		var batch []candidate
		for _, c := range candidates {
			if len(batch) >= alpha {
				break
			}
			key := c.ni.Addr.String()
			if queried[key] {
				continue
			}
			batch = append(batch, c)
		}
		if len(batch) == 0 {
			break
		}

		// Query candidates in parallel (limited by alpha).
		type roundResult struct {
			err error
			msg *krpc.Msg
			c   candidate
		}
		resultCh := make(chan roundResult, len(batch))

		for _, c := range batch {
			c := c
			key := c.ni.Addr.String()
			mu.Lock()
			queried[key] = true
			mu.Unlock()

			go func() {
				addr := dht.NewAddr(&net.UDPAddr{IP: c.ni.Addr.IP, Port: c.ni.Addr.Port})
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()

				res := s.Server.Get(ctx, addr, target, seq,
					dht.QueryRateLimiting{NotFirst: true})
				if res.ToError() != nil {
					resultCh <- roundResult{err: res.Err, c: c}
					return
				}
				if res.Reply.R == nil {
					resultCh <- roundResult{err: fmt.Errorf("empty reply"), c: c}
					return
				}
				resultCh <- roundResult{msg: &res.Reply, c: c}
			}()
		}

		// Collect results.
		for i := 0; i < len(batch); i++ {
			rr := <-resultCh
			if rr.err != nil || rr.msg == nil || rr.msg.R == nil {
				continue
			}
			// If the response contains a value, we are done.
			if len(rr.msg.R.V) > 0 {
				return rr.msg, nil
			}
			// Otherwise, add returned nodes (closest to target) to candidates.
			for _, node := range rr.msg.R.Nodes {
				key := node.Addr.String()
				mu.Lock()
				already := queried[key]
				mu.Unlock()
				if already {
					continue
				}
				nodeInt160 := int160.FromByteArray(node.ID)
				dist := nodeInt160.Distance(targetInt160)
				mu.Lock()
				candidates = append(candidates, candidate{ni: node, dist: dist})
				mu.Unlock()
			}
			for _, node := range rr.msg.R.Nodes6 {
				key := node.Addr.String()
				mu.Lock()
				already := queried[key]
				mu.Unlock()
				if already {
					continue
				}
				nodeInt160 := int160.FromByteArray(node.ID)
				dist := nodeInt160.Distance(targetInt160)
				mu.Lock()
				candidates = append(candidates, candidate{ni: node, dist: dist})
				mu.Unlock()
			}
		}
	}

	return nil, ErrBEP44NotFound
}

// closestNodes returns up to count NodeInfo entries from the routing table
// that are closest to the given target.
func (s *BTDHTService) closestNodes(target [20]byte, count int) []krpc.NodeInfo {
	type nodeDist struct {
		ni   krpc.NodeInfo
		dist int160.T
	}

	targetInt160 := int160.FromByteArray(target)
	startNodes, err := s.Server.TraversalStartingNodes()
	if err != nil || len(startNodes) == 0 {
		return nil
	}

	var nds []nodeDist
	for _, n := range startNodes {
		if !n.Id.Ok {
			continue
		}
		id := n.Id.Value
		ni := krpc.NodeInfo{
			ID:   id.AsByteArray(),
			Addr: n.Addr.ToNodeAddr(),
		}
		dist := id.Distance(targetInt160)
		nds = append(nds, nodeDist{ni: ni, dist: dist})
	}

	sort.Slice(nds, func(i, j int) bool {
		return nds[i].dist.Cmp(nds[j].dist) < 0
	})

	if len(nds) > count {
		nds = nds[:count]
	}
	result := make([]krpc.NodeInfo, len(nds))
	for i, nd := range nds {
		result[i] = nd.ni
	}
	return result
}

// decodeBEP44Value decodes a bencoded BEP 44 value field. For values that
// were stored as bencode.Bytes (a byte string), it returns the raw bytes.
func decodeBEP44Value(v bencode.Bytes) ([]byte, error) {
	if len(v) == 0 {
		return nil, ErrBEP44NotFound
	}
	// The v field contains a bencoded byte string. Decode it.
	var raw []byte
	if err := bencode.Unmarshal(v, &raw); err != nil {
		// The value might not be a plain byte string (e.g. a dict); return
		// the raw bencoded bytes in that case.
		return []byte(v), nil
	}
	return raw, nil
}

// MakeBEP44Key 生成适用于 BEP 44 可变项的新 Ed25519 密钥对。
func MakeBEP44Key() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return nil, nil, fmt.Errorf("generate ed25519 key: %w", err)
	}
	return pub, priv, nil
}

// MakeBEP44Target 根据公钥和可选 salt 计算可变 BEP 44 项目的目标值（infohash）。
func MakeBEP44Target(pubKey ed25519.PublicKey, salt []byte) [20]byte {
	var pk [32]byte
	copy(pk[:], pubKey)
	return bep44.MakeMutableTarget(pk, salt)
}
