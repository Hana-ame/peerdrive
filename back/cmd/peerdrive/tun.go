package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// tun.go — PeerJS-based port forwarding and P2P tunnel CLI (Issue #245, referencing p2ptun).
//
// Background & Design:
// Provides an easy-to-use CLI interface modeled after p2ptun to establish peer-to-peer tunnels
// across NATs using WebRTC DataChannels:
// 1. connect: Maps a remote node's service port to a local loopback port via DataChannel.
// 2. expose: Publishes a local port authorization rule with an auth key (whitelisting).
// 3. list: Displays active tunnels, local proxy listeners, and traffic statistics.

func runTun(args []string) {
	if len(args) == 0 {
		tunUsage()
		os.Exit(1)
	}

	sub := args[0]
	subArgs := args[1:]

	switch sub {
	case "connect":
		runTunConnect(subArgs)
	case "expose", "rule", "serve":
		runTunExpose(subArgs)
	case "list", "status":
		runTunList(subArgs)
	case "help", "--help", "-h":
		tunUsage()
	default:
		// If first arg starts with flag (e.g. -peer), treat as connect mode
		if strings.HasPrefix(sub, "-") {
			runTunConnect(args)
		} else {
			fmt.Fprintf(os.Stderr, "peerdrive tun: unknown action %q\n\n", sub)
			tunUsage()
			os.Exit(1)
		}
	}
}

func defaultAPIBase() string {
	if base := os.Getenv("PEERDRIVE_API_BASE"); base != "" {
		return strings.TrimRight(base, "/")
	}
	port := os.Getenv("PEERDRIVE_PORT")
	if port == "" {
		port = os.Getenv("PORT")
	}
	if port == "" {
		port = "3000"
	}
	return fmt.Sprintf("http://127.0.0.1:%s", port)
}

func runTunConnect(args []string) {
	fs := flag.NewFlagSet("tun connect", flag.ExitOnError)
	peer := fs.String("peer", "", "target peer ID to connect to")
	localPort := fs.Int("local", 0, "local port to listen on (e.g. 8080)")
	remotePort := fs.Int("remote", 0, "remote target port (0 = use server rule default)")
	key := fs.String("key", "", "authorization key for the target port rule")
	api := fs.String("api", defaultAPIBase(), "peerdrive daemon API base URL")
	_ = fs.Parse(args)

	if *peer == "" {
		fmt.Fprintln(os.Stderr, "error: -peer is required")
		fs.Usage()
		os.Exit(1)
	}
	if *localPort <= 0 || *localPort > 65535 {
		fmt.Fprintln(os.Stderr, "error: -local port must be between 1 and 65535")
		fs.Usage()
		os.Exit(1)
	}
	if *key == "" {
		fmt.Fprintln(os.Stderr, "error: -key is required")
		fs.Usage()
		os.Exit(1)
	}

	payload, _ := json.Marshal(map[string]any{
		"target_peer": *peer,
		"local_port":  *localPort,
		"port":        *remotePort,
		"key":         *key,
	})

	url := fmt.Sprintf("%s/p2p/forward/connect", *api)
	resp, err := http.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		fmt.Fprintf(os.Stderr, "[P2PTUN] connect failed: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "[P2PTUN] connect failed (status %d): %s\n", resp.StatusCode, string(bodyBytes))
		os.Exit(1)
	}

	fmt.Printf("[P2PTUN] Tunnel established!\n")
	fmt.Printf("[P2PTUN] Local proxy listening on: 127.0.0.1:%d\n", *localPort)
	fmt.Printf("[P2PTUN] Forwarding traffic -> Peer: %s (Remote Port: %d)\n", *peer, *remotePort)
	fmt.Println("[P2PTUN] Streaming over WebRTC DataChannel. Press Ctrl+C to disconnect.")

	// Wait for interrupt
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	fmt.Println("\n[P2PTUN] Closing tunnel...")
	closePayload, _ := json.Marshal(map[string]any{
		"key": *key,
	})
	closeURL := fmt.Sprintf("%s/p2p/forward/close", *api)
	_, _ = http.Post(closeURL, "application/json", bytes.NewReader(closePayload))
	fmt.Println("[P2PTUN] Tunnel closed.")
}

func runTunExpose(args []string) {
	fs := flag.NewFlagSet("tun expose", flag.ExitOnError)
	port := fs.Int("port", 0, "local port to expose (e.g. 8080)")
	key := fs.String("key", "", "authorization secret key")
	api := fs.String("api", defaultAPIBase(), "peerdrive daemon API base URL")
	_ = fs.Parse(args)

	if *port <= 0 || *port > 65535 {
		fmt.Fprintln(os.Stderr, "error: -port must be between 1 and 65535")
		fs.Usage()
		os.Exit(1)
	}
	if *key == "" {
		fmt.Fprintln(os.Stderr, "error: -key is required")
		fs.Usage()
		os.Exit(1)
	}

	payload, _ := json.Marshal(map[string]any{
		"port": *port,
		"key":  *key,
	})

	url := fmt.Sprintf("%s/p2p/forward/create", *api)
	resp, err := http.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		fmt.Fprintf(os.Stderr, "[P2PTUN] expose failed: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "[P2PTUN] expose failed (status %d): %s\n", resp.StatusCode, string(bodyBytes))
		os.Exit(1)
	}

	fmt.Printf("[P2PTUN] Rule registered successfully!\n")
	fmt.Printf("[P2PTUN] Port 127.0.0.1:%d is now authorized for remote peers presenting key %s\n", *port, *key)
}

func runTunList(args []string) {
	fs := flag.NewFlagSet("tun list", flag.ExitOnError)
	api := fs.String("api", defaultAPIBase(), "peerdrive daemon API base URL")
	_ = fs.Parse(args)

	url := fmt.Sprintf("%s/p2p/forward/list", *api)
	resp, err := http.Get(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[P2PTUN] list failed: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	var data struct {
		Listeners []struct {
			ID         string `json:"id"`
			TargetPeer string `json:"target_peer"`
			LocalPort  string `json:"local_port"`
		} `json:"listeners"`
		Tunnels []struct {
			PeerID    string    `json:"peer_id"`
			Port      int       `json:"port"`
			KeyID     string    `json:"key_id"`
			BytesIn   uint64    `json:"bytes_in"`
			BytesOut  uint64    `json:"bytes_out"`
			CreatedAt time.Time `json:"created_at"`
		} `json:"tunnels"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		fmt.Fprintf(os.Stderr, "[P2PTUN] parse response failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("=== Active Local Proxy Listeners ===")
	if len(data.Listeners) == 0 {
		fmt.Println("  (No active local proxy listeners)")
	} else {
		for _, l := range data.Listeners {
			fmt.Printf("  • %s -> Target Peer: %s\n", l.LocalPort, l.TargetPeer)
		}
	}

	fmt.Println("\n=== Active Forwarding Tunnels ===")
	if len(data.Tunnels) == 0 {
		fmt.Println("  (No active forwarding tunnels)")
	} else {
		for _, t := range data.Tunnels {
			fmt.Printf("  • Peer: %s | Port: %d | Key: %s | In: %d B | Out: %d B\n",
				t.PeerID, t.Port, t.KeyID, t.BytesIn, t.BytesOut)
		}
	}
}

func tunUsage() {
	fmt.Fprint(os.Stderr, `peerdrive tun — 基于 PeerJS 互联的端口转发与内网穿透（参考 p2ptun）

用法:
  peerdrive tun connect -peer <peerID> -local <localPort> -remote <remotePort> -key <key>
    建立本地监听器并桥接至目标对端端口（客户端模式）

  peerdrive tun expose -port <port> -key <key>
    开放本端服务的转发白名单规则（服务端模式）

  peerdrive tun list
    查询当前活跃的代理监听器与流式隧道状态
`)
}
