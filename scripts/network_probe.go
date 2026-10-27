package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"
)

type ProbeResult struct {
	Category string
	Target   string
	Status   string // PASS, FAIL, SKIP
	Latency  time.Duration
	Details  string
}

func main() {
	var results []ProbeResult
	fmt.Println("=== GitHub Actions / Host Network Diagnostic Probe ===")

	// 1. Host and Interface Info
	envInfo := gatherEnvInfo()
	fmt.Printf("OS/Arch: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Printf("DNS Resolvers: %s\n", envInfo["dns"])
	fmt.Printf("Local Non-loopback IPs: %s\n", envInfo["local_ips"])

	// 2. Public IP Check
	pubIP, pubLat, pubErr := checkPublicIP()
	if pubErr != nil {
		results = append(results, ProbeResult{
			Category: "Public IP",
			Target:   "api.ipify.org",
			Status:   "FAIL",
			Latency:  pubLat,
			Details:  pubErr.Error(),
		})
	} else {
		results = append(results, ProbeResult{
			Category: "Public IP",
			Target:   "api.ipify.org",
			Status:   "PASS",
			Latency:  pubLat,
			Details:  fmt.Sprintf("External IPv4: %s", pubIP),
		})
	}

	// 3. DNS Resolution
	dnsTargets := []string{
		"0.peerjs.com",
		"broker.emqx.io",
		"stun.l.google.com",
		"peersignal.moonchan.xyz",
		"api.github.com",
	}
	for _, host := range dnsTargets {
		ips, lat, err := probeDNS(host)
		if err != nil {
			results = append(results, ProbeResult{
				Category: "DNS Lookup",
				Target:   host,
				Status:   "FAIL",
				Latency:  lat,
				Details:  err.Error(),
			})
		} else {
			results = append(results, ProbeResult{
				Category: "DNS Lookup",
				Target:   host,
				Status:   "PASS",
				Latency:  lat,
				Details:  strings.Join(ips, ", "),
			})
		}
	}

	// 4. TCP & TLS Connectivity
	tcpTargets := []struct {
		address string
		useTLS  bool
		sni     string
	}{
		{"broker.emqx.io:1883", false, ""},
		{"0.peerjs.com:443", true, "0.peerjs.com"},
		{"peersignal.moonchan.xyz:443", true, "peersignal.moonchan.xyz"},
		{"api.github.com:443", true, "api.github.com"},
	}

	for _, tt := range tcpTargets {
		lat, det, err := probeTCP(tt.address, tt.useTLS, tt.sni)
		category := "TCP Connection"
		if tt.useTLS {
			category = "TCP+TLS Handshake"
		}
		if err != nil {
			results = append(results, ProbeResult{
				Category: category,
				Target:   tt.address,
				Status:   "FAIL",
				Latency:  lat,
				Details:  err.Error(),
			})
		} else {
			results = append(results, ProbeResult{
				Category: category,
				Target:   tt.address,
				Status:   "PASS",
				Latency:  lat,
				Details:  det,
			})
		}
	}

	// 5. UDP STUN RFC 5389 Binding
	stunTargets := []string{
		"stun.l.google.com:19302",
		"stun1.l.google.com:19302",
		"stun.sipgate.net:3478",
	}
	for _, st := range stunTargets {
		lat, mappedAddr, err := probeSTUN(st)
		if err != nil {
			results = append(results, ProbeResult{
				Category: "UDP STUN (WebRTC)",
				Target:   st,
				Status:   "FAIL",
				Latency:  lat,
				Details:  err.Error(),
			})
		} else {
			results = append(results, ProbeResult{
				Category: "UDP STUN (WebRTC)",
				Target:   st,
				Status:   "PASS",
				Latency:  lat,
				Details:  fmt.Sprintf("Reflexive Mapped Address: %s", mappedAddr),
			})
		}
	}

	// 6. HTTP Endpoint Status
	httpTargets := []string{
		"https://0.peerjs.com/",
		"https://peersignal.moonchan.xyz/",
	}
	for _, u := range httpTargets {
		code, lat, err := probeHTTP(u)
		if err != nil {
			results = append(results, ProbeResult{
				Category: "HTTP Service",
				Target:   u,
				Status:   "FAIL",
				Latency:  lat,
				Details:  err.Error(),
			})
		} else {
			results = append(results, ProbeResult{
				Category: "HTTP Service",
				Target:   u,
				Status:   "PASS",
				Latency:  lat,
				Details:  fmt.Sprintf("HTTP Status %d", code),
			})
		}
	}

	// 7. Output Report
	markdown := generateReport(envInfo, results)
	fmt.Println("\n" + markdown)

	_ = os.WriteFile("network_probe_results.md", []byte(markdown), 0644)
	if stepSummary := os.Getenv("GITHUB_STEP_SUMMARY"); stepSummary != "" {
		_ = os.WriteFile(stepSummary, []byte(markdown), 0644)
	}
}

func gatherEnvInfo() map[string]string {
	info := make(map[string]string)
	// Nameservers
	if data, err := os.ReadFile("/etc/resolv.conf"); err == nil {
		var ns []string
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "nameserver") {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					ns = append(ns, fields[1])
				}
			}
		}
		info["dns"] = strings.Join(ns, ", ")
	} else {
		info["dns"] = "Unknown"
	}

	// Local IPs
	var ips []string
	if ifaces, err := net.Interfaces(); err == nil {
		for _, iface := range ifaces {
			if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
				continue
			}
			addrs, err := iface.Addrs()
			if err != nil {
				continue
			}
			for _, addr := range addrs {
				if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() {
					ips = append(ips, fmt.Sprintf("%s (%s)", ipNet.IP.String(), iface.Name))
				}
			}
		}
	}
	info["local_ips"] = strings.Join(ips, ", ")
	return info
}

func checkPublicIP() (string, time.Duration, error) {
	client := &http.Client{Timeout: 4 * time.Second}
	start := time.Now()
	resp, err := client.Get("https://api.ipify.org")
	lat := time.Since(start)
	if err != nil {
		return "", lat, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", lat, err
	}
	return strings.TrimSpace(string(body)), lat, nil
}

func probeDNS(host string) ([]string, time.Duration, error) {
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupHost(ctx, host)
	lat := time.Since(start)
	return addrs, lat, err
}

func probeTCP(address string, useTLS bool, sni string) (time.Duration, string, error) {
	start := time.Now()
	conn, err := net.DialTimeout("tcp", address, 4*time.Second)
	if err != nil {
		return time.Since(start), "", err
	}
	defer conn.Close()
	tcpLat := time.Since(start)

	if !useTLS {
		return tcpLat, fmt.Sprintf("TCP Connected (RTT: %v)", tcpLat.Round(time.Millisecond)), nil
	}

	tlsConn := tls.Client(conn, &tls.Config{
		ServerName: sni,
		MinVersion: tls.VersionTLS12,
	})
	_ = tlsConn.SetDeadline(time.Now().Add(4 * time.Second))
	tlsStart := time.Now()
	if err := tlsConn.Handshake(); err != nil {
		return time.Since(start), "", fmt.Errorf("TLS handshake error: %w", err)
	}
	tlsLat := time.Since(tlsStart)
	state := tlsConn.ConnectionState()
	versionStr := "TLS"
	switch state.Version {
	case tls.VersionTLS13:
		versionStr = "TLS 1.3"
	case tls.VersionTLS12:
		versionStr = "TLS 1.2"
	}
	details := fmt.Sprintf("TCP: %v, %s Handshake: %v (ALPN: %s)",
		tcpLat.Round(time.Millisecond),
		versionStr,
		tlsLat.Round(time.Millisecond),
		state.NegotiatedProtocol,
	)
	return time.Since(start), details, nil
}

// probeSTUN sends an RFC 5389 Binding Request and reads XOR-MAPPED-ADDRESS
func probeSTUN(server string) (time.Duration, string, error) {
	raddr, err := net.ResolveUDPAddr("udp", server)
	if err != nil {
		return 0, "", fmt.Errorf("resolve error: %w", err)
	}

	conn, err := net.DialUDP("udp", nil, raddr)
	if err != nil {
		return 0, "", fmt.Errorf("dial UDP error: %w", err)
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))

	// STUN Header (20 bytes)
	// Type: 0x0001 (Binding Request)
	// Length: 0x0000
	// Magic Cookie: 0x2112A442
	// Transaction ID: 12 random bytes
	req := make([]byte, 20)
	binary.BigEndian.PutUint16(req[0:2], 0x0001)
	binary.BigEndian.PutUint16(req[2:4], 0x0000)
	binary.BigEndian.PutUint32(req[4:8], 0x2112A442)
	if _, err := rand.Read(req[8:20]); err != nil {
		return 0, "", err
	}

	start := time.Now()
	if _, err := conn.Write(req); err != nil {
		return time.Since(start), "", fmt.Errorf("send error: %w", err)
	}

	resp := make([]byte, 1024)
	n, err := conn.Read(resp)
	lat := time.Since(start)
	if err != nil {
		return lat, "", fmt.Errorf("read/timeout error (UDP likely blocked/unreachable): %w", err)
	}

	if n < 20 {
		return lat, "", fmt.Errorf("packet too short (%d bytes)", n)
	}

	msgType := binary.BigEndian.Uint16(resp[0:2])
	if msgType != 0x0101 { // Binding Success Response
		return lat, "", fmt.Errorf("unexpected STUN response type: 0x%04x", msgType)
	}

	// Verify Transaction ID
	if !strings.EqualFold(string(resp[8:20]), string(req[8:20])) {
		return lat, "", fmt.Errorf("transaction ID mismatch")
	}

	// Parse attributes looking for XOR-MAPPED-ADDRESS (0x0020) or MAPPED-ADDRESS (0x0001)
	mappedAddr := parseSTUNMappedAddress(resp[20:n])
	if mappedAddr == "" {
		return lat, "Response received, but XOR-MAPPED-ADDRESS not found", nil
	}
	return lat, mappedAddr, nil
}

func parseSTUNMappedAddress(attrs []byte) string {
	offset := 0
	for offset+4 <= len(attrs) {
		attrType := binary.BigEndian.Uint16(attrs[offset : offset+2])
		attrLen := int(binary.BigEndian.Uint16(attrs[offset+2 : offset+4]))
		offset += 4
		if offset+attrLen > len(attrs) {
			break
		}
		data := attrs[offset : offset+attrLen]
		// XOR-MAPPED-ADDRESS: 0x0020
		if attrType == 0x0020 && len(data) >= 8 {
			family := data[1]
			port := binary.BigEndian.Uint16(data[2:4]) ^ 0x2112
			if family == 0x01 && len(data) >= 8 { // IPv4
				ipBytes := make([]byte, 4)
				copy(ipBytes, data[4:8])
				magic := []byte{0x21, 0x12, 0xa4, 0x42}
				for i := 0; i < 4; i++ {
					ipBytes[i] ^= magic[i]
				}
				return fmt.Sprintf("%s:%d", net.IP(ipBytes).String(), port)
			}
		}
		// Pad to 4 bytes boundary
		pad := (4 - (attrLen % 4)) % 4
		offset += attrLen + pad
	}
	return ""
}

func probeHTTP(targetURL string) (int, time.Duration, error) {
	client := &http.Client{Timeout: 4 * time.Second}
	start := time.Now()
	resp, err := client.Get(targetURL)
	lat := time.Since(start)
	if err != nil {
		return 0, lat, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, lat, nil
}

func generateReport(envInfo map[string]string, results []ProbeResult) string {
	var b strings.Builder
	b.WriteString("## 🌐 GitHub Actions / Network Connectivity Probe Report\n\n")
	b.WriteString(fmt.Sprintf("**Executed At:** %s (UTC)\n\n", time.Now().UTC().Format(time.RFC3339)))
	b.WriteString(fmt.Sprintf("- **Platform:** `%s/%s`\n", runtime.GOOS, runtime.GOARCH))
	b.WriteString(fmt.Sprintf("- **DNS Nameservers:** `%s`\n", envInfo["dns"]))
	b.WriteString(fmt.Sprintf("- **Internal IPs:** `%s`\n\n", envInfo["local_ips"]))

	b.WriteString("### 📊 Diagnostic Results\n\n")
	b.WriteString("| Category | Target | Status | Latency | Details |\n")
	b.WriteString("| :--- | :--- | :---: | :---: | :--- |\n")
	for _, r := range results {
		icon := "✅ PASS"
		if r.Status == "FAIL" {
			icon = "❌ FAIL"
		} else if r.Status == "SKIP" {
			icon = "⚠️ SKIP"
		}
		b.WriteString(fmt.Sprintf("| %s | `%s` | %s | %v | %s |\n",
			r.Category, r.Target, icon, r.Latency.Round(time.Millisecond), r.Details))
	}

	b.WriteString("\n### 🔍 Protocol Insights\n")
	allPass := true
	for _, r := range results {
		if r.Status == "FAIL" {
			allPass = false
			break
		}
	}
	if allPass {
		b.WriteString("- **Overall Status**: All target services (DNS, HTTPS, TCP, UDP/STUN) are reachable without blocking.\n")
		b.WriteString("- **UDP/STUN**: Outbound UDP is fully open and reflexive NAT mapping succeeds. WebRTC ICE peer discovery can communicate externally.\n")
	} else {
		b.WriteString("- **Overall Status**: Some external services failed or timed out. Review the specific categories above.\n")
	}

	return b.String()
}
