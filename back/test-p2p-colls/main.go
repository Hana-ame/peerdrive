package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"peerdrive/internal/config"
	"peerdrive/internal/model"
	"peerdrive/internal/service"

	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
)

type collectionListResponse struct {
	UserCollections []model.Collection            `json:"user_collections"`
	AnonCollections []model.AnonCollectionSummary `json:"anon_collections"`
}

func main() {
	cfg := config.Load()
	cfg.P2PEnable = true
	cfg.P2PListenAddr = "/ip4/0.0.0.0/tcp/0"
	cfg.P2PRelayMode = config.RelayClient
	cfg.P2PStaticRelays = ""
	cfg.P2PHolePunch = true
	cfg.P2PAutoNAT = true
	cfg.P2PMDNSEnable = false
	cfg.StorageDir = "/tmp/pd-test-storage"

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	p2pSvc, err := service.NewP2PService(ctx, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: %v\n", err)
		os.Exit(1)
	}
	defer p2pSvc.Close()

	peerID, addrs := p2pSvc.GetNodeInfo()
	fmt.Printf("Local PeerID: %s\n", peerID)
	fmt.Printf("Local Addrs: %v\n\n", addrs)

	// Connect to VPS relay
	maddr, err := ma.NewMultiaddr("/ip4/97.64.30.221/tcp/4001")
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse addr: %v\n", err)
		os.Exit(1)
	}
	targetPeer, err := peer.Decode("12D3KooWSbj9NsY2bBY1BeorgWWmqZypjiqpEHTLNSVNMhWuMVRZ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "decode peer: %v\n", err)
		os.Exit(1)
	}

	info := peer.AddrInfo{ID: targetPeer, Addrs: []ma.Multiaddr{maddr}}
	err = p2pSvc.Host.Connect(ctx, info)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Connected to VPS: %s\n\n", targetPeer)

	// Test RequestCollectionList (empty query)
	fmt.Println("=== RequestCollectionList (empty query) ===")
	data, err := p2pSvc.RequestCollectionList(ctx, targetPeer, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "RequestCollectionList error: %v\n", err)
		os.Exit(1)
	}
	var resp collectionListResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		fmt.Fprintf(os.Stderr, "unmarshal error: %v\n", err)
		fmt.Fprintf(os.Stderr, "raw data: %s\n", string(data))
		os.Exit(1)
	}
	fmt.Printf("User collections: %d\n", len(resp.UserCollections))
	for _, c := range resp.UserCollections {
		h := "<nil>"
		if c.CurrentHash != nil {
			h = *c.CurrentHash
		}
		fmt.Printf("  - %s/%s (visibility=%s, hash=%s)\n", c.Username, c.CollectionName, c.Visibility, h)
	}
	fmt.Printf("Anon collections: %d\n", len(resp.AnonCollections))
	for _, c := range resp.AnonCollections {
		fmt.Printf("  - hash=%s, name=%s, entries=%d\n", c.Hash, c.FriendlyName, c.EntryCount)
	}

	// Test with search query
	fmt.Println("\n=== RequestCollectionList (query: test) ===")
	data2, err := p2pSvc.RequestCollectionList(ctx, targetPeer, "test")
	if err != nil {
		fmt.Fprintf(os.Stderr, "RequestCollectionList error: %v\n", err)
		os.Exit(1)
	}
	var resp2 collectionListResponse
	json.Unmarshal(data2, &resp2)
	total := len(resp2.UserCollections) + len(resp2.AnonCollections)
	fmt.Printf("Found %d total collections matching 'test'\n", total)

	fmt.Println("\nPASSED: P2P collection list protocol works!")
}
