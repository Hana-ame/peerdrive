// peerserver: Standalone public infrastructure server unifying PeerJS signaling,
// room discovery, BitTorrent HTTP tracker, account auth, and Node DNS / relay registration.
//
// Single port, lightweight, zero CAS file engine, zero peerdrive.db.
//
// Endpoints mounted:
//   - Signaling & discovery: /peerjs, /peerjs/id, /discover/*, /status, /status/key, / (dashboard)
//   - BitTorrent HTTP Tracker: /announce, /scrape, /tracker/bans
//   - Auth & Node DNS: /auth/*, /p2p/relay/*, /ping, /api/health
package main

import (
	"crypto/subtle"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/Hana-ame/go-peerserver"
	"github.com/Hana-ame/go-peerserver/regserver"
	"github.com/Hana-ame/go-peerserver/tracker"
)

func main() {
	addr := flag.String("addr", ":9000", "listen address")
	key := flag.String("key", signalserver.DefaultKey, "PeerJS API key (client must match; default is authoritative key)")
	tokens := flag.String("tokens", "", "signaling token whitelist (comma-separated; empty = no restriction)")
	opsToken := flag.String("ops-token", "", "credential for ops endpoints (/status, /status/key). Empty = ops endpoints reject")
	tlsCert := flag.String("tls-cert", "", "TLS certificate (PEM). When given together with -tls-key, serve over HTTPS/WSS")
	tlsKey := flag.String("tls-key", "", "TLS private key (PEM)")
	corsOrigin := flag.String("cors-origin", "", "comma-separated CORS allow-list for panel-facing REST endpoints")

	// Rate limiting for signaling
	rateAnnounce := flag.Float64("rate-announce", 1, "per-IP requests/s for POST /discover/announce (0 = unlimited)")
	rateAnnounceBurst := flag.Int("rate-announce-burst", 10, "burst for -rate-announce")
	rateWS := flag.Float64("rate-ws", 2, "per-IP WebSocket upgrades/s on /peerjs (0 = unlimited)")
	rateWSBurst := flag.Int("rate-ws-burst", 20, "burst for -rate-ws")
	rateID := flag.Float64("rate-id", 5, "per-IP requests/s for GET /peerjs/id (0 = unlimited)")
	rateIDBurst := flag.Int("rate-id-burst", 20, "burst for -rate-id")

	// BitTorrent tracker flags
	btTracker := flag.Bool("bt-tracker", false, "enable BitTorrent HTTP tracker (/announce, /scrape)")
	btBansFile := flag.String("bt-bans-file", "tracker_bans.json", "JSON file for tracker ban persistence")
	btInterval := flag.Int("bt-interval", 900, "announce interval in seconds (default 900 = 15 min)")
	btMaxPeers := flag.Int("bt-max-peers", 100, "max peers per info_hash (default 100)")

	// Regserver database and JWT flags
	dbPath := flag.String("db", "", "sqlite path for regserver (default: $PEERDRIVE_REG_DB -> $DB_PATH -> ./reg.db)")
	jwtSecret := flag.String("jwt-secret", "", "JWT secret for regserver (default: $PEERDRIVE_JWT_SECRET -> $JWT_SECRET)")

	flag.Parse()

	if *jwtSecret != "" {
		_ = os.Setenv("PEERDRIVE_JWT_SECRET", *jwtSecret)
	}

	var sigOpts []signalserver.Option
	if *tokens != "" {
		sigOpts = append(sigOpts, signalserver.WithTokenWhitelist(strings.Split(*tokens, ",")))
	}
	if *opsToken != "" {
		sigOpts = append(sigOpts, signalserver.WithOpsToken(*opsToken))
	}
	if *corsOrigin != "" {
		sigOpts = append(sigOpts, signalserver.WithCORSOrigins(strings.Split(*corsOrigin, ",")))
		log.Printf("cors: restricted to %s", *corsOrigin)
	}
	if *rateAnnounce > 0 || *rateWS > 0 || *rateID > 0 {
		sigOpts = append(sigOpts, signalserver.WithRateLimit(signalserver.RateLimitConfig{
			AnnounceRPS: *rateAnnounce, AnnounceBurst: *rateAnnounceBurst,
			WSRPS: *rateWS, WSBurst: *rateWSBurst,
			IDRPS: *rateID, IDBurst: *rateIDBurst,
		}))
	}

	sigSrv := signalserver.NewServer(*key, sigOpts...)
	sigSrv.Start()

	// Initialize regserver (auth, relay registration, Node DNS)
	regSrv, err := regserver.New(*dbPath)
	if err != nil {
		log.Fatalf("peerserver: failed to initialize regserver: %v", err)
	}
	defer regSrv.Close()

	// If BitTorrent tracker enabled, wire it through regserver (supports user bans mapped to JWT)
	if *btTracker {
		btOpts := []tracker.Option{
			tracker.WithAnnounceInterval(*btInterval),
			tracker.WithMaxPeers(*btMaxPeers),
		}
		if *btBansFile != "" {
			btOpts = append(btOpts, tracker.WithBanFile(*btBansFile))
		}
		if *opsToken != "" {
			btOpts = append(btOpts, tracker.WithBanAuth(btBanAuth(*opsToken)))
		}
		tr := regSrv.SetupTracker(btOpts...)
		regSrv.SetTracker(tr)
		log.Printf("bt-tracker: enabled (/announce, /scrape, /tracker/bans)")
	}

	mux := http.NewServeMux()

	// Mount signaling routes
	mux.HandleFunc("/peerjs", sigSrv.HandleWS)
	mux.HandleFunc("/peerjs/id", sigSrv.HandleID)
	mux.HandleFunc("/discover/announce", sigSrv.HandleAnnounce)
	mux.HandleFunc("/discover/leave", sigSrv.HandleLeave)
	mux.HandleFunc("/discover/nodes", sigSrv.HandleNodes)
	mux.HandleFunc("/status", sigSrv.HandleStatus)
	mux.HandleFunc("/status/key", sigSrv.HandleOpsKey)

	// Mount regserver routes (auth, relay, ping, health, and tracker if enabled)
	regMux := regSrv.Handler()
	regPatterns := []string{
		"GET /ping", "GET /api/health",
		"POST /auth/register", "POST /auth/login",
		"GET /auth/whoami", "GET /auth/list",
		"POST /p2p/relay/register", "POST /p2p/relay/heartbeat",
		"GET /p2p/relay/list",
	}
	for _, p := range regPatterns {
		mux.Handle(p, regMux)
	}
	if *btTracker {
		mux.Handle("/announce", regMux)
		mux.Handle("/scrape", regMux)
		mux.Handle("/tracker/bans", regMux)
	}

	// Status dashboard at "/" catch-all
	mux.HandleFunc("/", sigSrv.HandleDashboard)

	log.Printf("peerserver: listening on %s (signaling + room discovery + tracker + regserver/relay)", *addr)
	if err := Serve(*addr, *tlsCert, *tlsKey, mux); err != nil {
		log.Fatal(err)
	}
}

func btBanAuth(opsToken string) func(*http.Request) bool {
	return func(r *http.Request) bool {
		if opsToken == "" {
			return false
		}
		var presented string
		if t := r.URL.Query().Get("token"); t != "" {
			presented = t
		} else if h := r.Header.Get("Authorization"); len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
			presented = strings.TrimSpace(h[7:])
		}
		if presented == "" {
			return false
		}
		return subtle.ConstantTimeCompare([]byte(presented), []byte(opsToken)) == 1
	}
}

func Serve(addr, certFile, keyFile string, h http.Handler) error {
	addr = regserver.NormalizePort(addr)
	if certFile == "" && keyFile == "" {
		return http.ListenAndServe(addr, h)
	}
	if certFile == "" || keyFile == "" {
		return fmt.Errorf("TLS requires both -tls-cert and -tls-key (current cert=%q key=%q)", certFile, keyFile)
	}
	return http.ListenAndServeTLS(addr, certFile, keyFile, h)
}
