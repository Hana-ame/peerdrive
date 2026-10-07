// Peersignal: self-hosted PeerJS signaling server + built-in room discovery.
// Replaces public cloud signaling (0.peerjs.com) and public MQTT brokers—nodes just need to point
// PEERDRIVE_PEERJS_HOST/PORT at this server; discovery goes through the built-in HTTP API.
//
// Usage: peersignal [-addr :9000] [-key peerjs] [-tokens tok1,tok2] [-tls-cert c.pem -tls-key k.pem]
//
//	-tokens optional: signaling token whitelist (comma-separated). When set, the WS connection token
//	must be on the list, otherwise the upgrade is rejected (prevents arbitrary clients from impersonating nodes to receive signaling).
//	-tls-cert/-tls-key optional: serve over HTTPS/WSS when both are given.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/Hana-ame/go-peerserver"
)

func main() {
	addr := flag.String("addr", ":9000", "listen address")
	key := flag.String("key", "peerjs", "API key (client must match)")
	tokens := flag.String("tokens", "", "signaling token whitelist (comma-separated; empty = no restriction)")
	opsToken := flag.String("ops-token", "",
		"credential for ops endpoints (/status, /status/key). Empty = ops endpoints reject everyone.\n"+
			"Kept separate from -tokens on purpose: -tokens also gates WebSocket registration, so\n"+
			"reusing it here would silently break any node that doesn't send a token.")
	tlsCert := flag.String("tls-cert", "", "TLS certificate (PEM). When given together with -tls-key, serve over HTTPS/WSS")
	tlsKey := flag.String("tls-key", "", "TLS private key (PEM)")
	corsOrigin := flag.String("cors-origin", "",
		"comma-separated CORS allow-list for the panel-facing REST endpoints "+
			"(e.g. https://peerdrive.pages.dev,null). Empty = keep the historical wildcard '*'")
	// Rate limiting (2026-10-06, N3), all per-source-IP; 0 disables one bucket
	// without touching the others.
	//
	// Defaults are sized for a small self-hosted relay. announce: nodes announce
	// every 30s (transport/http_discovery.go loop), so even 100 nodes total ~3.3 rps,
	// and since buckets are per-IP a single-node deployment uses 0.03 rps; burst 10
	// absorbs the thundering herd when many nodes restart together (a deploy, or a
	// host coming back at once). ws: 2 rps / burst 20 — sized for reconnects rather
	// than for steady-state connection count, because every reconnect re-enters the
	// upgrade path and each accepted one costs a goroutine + readLoop + table entry.
	rateAnnounce := flag.Float64("rate-announce", 1, "per-IP requests/s for POST /discover/announce (0 = unlimited)")
	rateAnnounceBurst := flag.Int("rate-announce-burst", 10, "burst for -rate-announce")
	rateWS := flag.Float64("rate-ws", 2, "per-IP WebSocket upgrades/s on /peerjs (0 = unlimited)")
	rateWSBurst := flag.Int("rate-ws-burst", 20, "burst for -rate-ws")
	rateID := flag.Float64("rate-id", 5, "per-IP requests/s for GET /peerjs/id (0 = unlimited)")
	rateIDBurst := flag.Int("rate-id-burst", 20, "burst for -rate-id")
	flag.Parse()

	var opts []signalserver.Option
	if *tokens != "" {
		opts = append(opts, signalserver.WithTokenWhitelist(strings.Split(*tokens, ",")))
	}
	if *opsToken != "" {
		opts = append(opts, signalserver.WithOpsToken(*opsToken))
	}
	if *corsOrigin != "" {
		opts = append(opts, signalserver.WithCORSOrigins(strings.Split(*corsOrigin, ",")))
		log.Printf("cors: restricted to %s (file:// panels need an explicit \"null\")", *corsOrigin)
	}
	if *rateAnnounce > 0 || *rateWS > 0 || *rateID > 0 {
		opts = append(opts, signalserver.WithRateLimit(signalserver.RateLimitConfig{
			AnnounceRPS: *rateAnnounce, AnnounceBurst: *rateAnnounceBurst,
			WSRPS: *rateWS, WSBurst: *rateWSBurst,
			IDRPS: *rateID, IDBurst: *rateIDBurst,
		}))
		log.Printf("rate limit per IP — announce %.2f/s (burst %d) · ws %.2f/s (burst %d) · id %.2f/s (burst %d)",
			*rateAnnounce, *rateAnnounceBurst, *rateWS, *rateWSBurst, *rateID, *rateIDBurst)
		log.Printf("note: clientIP() reads RemoteAddr only, so behind a reverse proxy every client shares " +
			"one bucket — limit per-client at the proxy instead")
	}
	srv := signalserver.NewServer(*key, opts...)
	srv.Start() // background sweeper: clean up expired offline queues (H3)

	mux := http.NewServeMux()
	// PeerJS-compatible signaling endpoints
	mux.HandleFunc("/peerjs", srv.HandleWS)
	mux.HandleFunc("/peerjs/id", srv.HandleID)
	// Built-in room discovery (replaces MQTT)
	mux.HandleFunc("/discover/announce", srv.HandleAnnounce)
	mux.HandleFunc("/discover/leave", srv.HandleLeave)
	mux.HandleFunc("/discover/nodes", srv.HandleNodes)
	// Status API and dashboard (graph visualization)
	mux.HandleFunc("/status", srv.HandleStatus)
	// 信令 key 单独一个端点：/status 不再回显它（2026-10-06）。
	// 同一个 -tokens 白名单做鉴权，没配 token 时默认进不来。
	mux.HandleFunc("/status/key", srv.HandleOpsKey)
	mux.HandleFunc("/", srv.HandleDashboard)

	if err := Serve(*addr, *tlsCert, *tlsKey, mux); err != nil {
		log.Fatal(err)
	}
}

// Serve serves on addr: uses HTTPS/WSS when both certFile and keyFile are given, otherwise HTTP/WS.
//
// Why TLS support is needed: the public panel (packages/peerdrive-client/dist/panel.html) is deployed on
// GitHub Pages, which enforces HTTPS. Browsers treat ws:// requests from HTTPS pages as
// mixed content and block them outright (on the PeerJS side it just looks like a connection failure with no obvious cause), so self-hosted signaling
// must use wss:// to be reachable from the public panel—either TLS directly in this process, or a reverse proxy in front.
func Serve(addr, certFile, keyFile string, h http.Handler) error {
	if certFile == "" && keyFile == "" {
		log.Printf("peerserver listening on %s (ws)", addr)
		return http.ListenAndServe(addr, h)
	}
	// Giving only one half is a typical typo: silently falling back to HTTP would cause the remote HTTPS page to be blocked by mixed content
	// interception, while the server appears to have "started normally", making it extremely hard to debug. Better to fail loudly.
	if certFile == "" || keyFile == "" {
		return fmt.Errorf("TLS requires both -tls-cert and -tls-key (current cert=%q key=%q)", certFile, keyFile)
	}
	log.Printf("peerserver listening on %s (wss)", addr)
	return http.ListenAndServeTLS(addr, certFile, keyFile, h)
}
