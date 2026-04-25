package service

import (
	"encoding/hex"
	"fmt"

	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p/core/peer"
	mh "github.com/multiformats/go-multihash"
	"github.com/multiformats/go-multiaddr"
)

func cidFromSha256(hash string) cid.Cid {
	if len(hash) != 64 {
		return cid.Undef
	}
	raw, err := hex.DecodeString(hash)
	if err != nil {
		return cid.Undef
	}
	mhash, err := mh.Encode(raw, mh.SHA2_256)
	if err != nil {
		return cid.Undef
	}
	return cid.NewCidV1(cid.Raw, mhash)
}

func multiaddrFromString(s string) (multiaddr.Multiaddr, error) {
	return multiaddr.NewMultiaddr(s)
}

func parsePeerAddr(s string) (*peer.AddrInfo, error) {
	maddr, err := multiaddr.NewMultiaddr(s)
	if err != nil {
		return nil, fmt.Errorf("invalid multiaddr: %w", err)
	}
	info, err := peer.AddrInfoFromP2pAddr(maddr)
	if err != nil {
		return nil, fmt.Errorf("parse peer info: %w", err)
	}
	return info, nil
}
