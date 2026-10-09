package peerjs

import (
	"github.com/Hana-ame/go-peerjs/signalling"
)

// Signaller is a signaling channel abstraction: responsible for node registration and message send/receive.
// Extensibility: the current implementation uses the PeerJS public cloud protocol (signalling.peerJSSignaller); it can later be replaced with
// self-hosted peerjs-server, MQTT room signaling, etc., without modifying the Peer/Connection layer.
//
// Note: when implementing a custom Signaller, message dispatch is not your concern — OnMessage is injected
// internally by the framework (called when Peer is constructed). The implementer only needs to call the
// injected callback after receiving a signaling message (see signalling.peerJSSignaller.readLoop for the pattern).
//
// The interface lives in the signalling subpackage (github.com/Hana-ame/go-peerjs/signalling),
// which owns the PeerJS-protocol transport; this alias keeps the historical import path working.
type Signaller = signalling.Signaller

// MessageHandler is a signaling message callback.
// Deprecated: users do not need to interact with it directly — message handling is taken over by Peer's internal router
// (OFFER/ANSWER/CANDIDATE/LEAVE/EXPIRE are all handled internally). This type is retained only as internal
// plumbing between the Signaller implementer and the framework.
type MessageHandler = signalling.MessageHandler

// SignallerFactory is the entry point for creating custom signaling (optional parameter to NewPeer).
type SignallerFactory = signalling.SignallerFactory
