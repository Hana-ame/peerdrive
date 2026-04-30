// IPFS 集成测试 — 启动本地 IPFS 节点，用 IPFSProvider 获取内容。
//
// 前置条件：系统已安装 ipfs (kubo) 命令行。
//
// 用法：
//
//	go run ./test/ipfs-integration/
//	IPFS_PATH=/custom/repo go run ./test/ipfs-integration/

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"peerdrive/internal/provider"
)

const (
	gatewayPort = "28080"
	apiPort     = "25001"
	gatewayURL  = "http://127.0.0.1:" + gatewayPort
)

var (
	testCID   string
	testData  = []byte(fmt.Sprintf("IPFS integration test %s\n", time.Now().Format(time.RFC3339)))
	testFile  string
	tmpDir    string
	repoDir   string
	daemonCmd *exec.Cmd
)

func main() {
	fmt.Println("==============================================")
	fmt.Println("  Peerdrive IPFS Gateway Integration Test")
	fmt.Println("==============================================")
	fmt.Println()

	exitCode := 0
	defer func() {
		cleanup()
		os.Exit(exitCode)
	}()

	// Step 1: Prerequisites
	fmt.Println("--- Step 1: Check prerequisites ---")
	if !checkPrereqs() {
		exitCode = 1
		return
	}

	// Step 2: Initialize temp IPFS node
	fmt.Println("\n--- Step 2: Init IPFS node ---")
	if !stepInit() {
		exitCode = 1
		return
	}

	// Step 3: Start IPFS daemon
	fmt.Println("\n--- Step 3: Start IPFS daemon ---")
	if !stepStartDaemon() {
		exitCode = 1
		return
	}

	// Step 4: Add test file to IPFS
	fmt.Println("\n--- Step 4: Add test file ---")
	if !stepAddFile() {
		exitCode = 1
		return
	}

	// Step 5: FetchByCID via IPFSProvider
	fmt.Println("\n--- Step 5: FetchByCID ---")
	if !stepFetchByCID() {
		exitCode = 1
	}

	// Step 6: GetReader via IPFSProvider
	fmt.Println("\n--- Step 6: GetReader ---")
	if !stepGetReader() {
		exitCode = 1
	}

	// Step 7: Race multiple gateways (local + bad)
	fmt.Println("\n--- Step 7: Race gateways ---")
	if !stepRaceGateways() {
		exitCode = 1
	}

	// Step 8: Context timeout
	fmt.Println("\n--- Step 8: Context timeout ---")
	if !stepContextTimeout() {
		exitCode = 1
	}

	// Step 9: Fetch non-existent CID
	fmt.Println("\n--- Step 9: Non-existent CID ---")
	if !stepNotFound() {
		exitCode = 1
	}

	// Summary
	fmt.Println()
	fmt.Println("==============================================")
	if exitCode == 0 {
		fmt.Println("  ALL IPFS INTEGRATION TESTS PASSED")
	} else {
		fmt.Println("  SOME TESTS FAILED")
	}
	fmt.Println("==============================================")
}

// ─── helpers ────────────────────────────────────────────────────────

func checkPrereqs() bool {
	if _, err := exec.LookPath("ipfs"); err != nil {
		fmt.Printf("  SKIP: ipfs binary not found (%v)\n", err)
		return false
	}
	out, _ := exec.Command("ipfs", "version").Output()
	fmt.Printf("  OK: %s", string(bytes.TrimSpace(out)))
	return true
}

func runIPFS(args ...string) (string, error) {
	cmd := exec.Command("ipfs", args...)
	cmd.Env = append(os.Environ(), "IPFS_PATH="+repoDir)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("%v: %s", err, stderr.String())
	}
	return strings.TrimSpace(stdout.String()), nil
}

func checkGateway() bool {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(gatewayURL + "/ipfs/QmUNLLsPACCz1vLxQVkXqqLX5R1X345qqfHbsf67hvA3Nn")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return true
}

func cleanup() {
	if daemonCmd != nil && daemonCmd.Process != nil {
		daemonCmd.Process.Kill()
		daemonCmd.Wait()
	}
	if tmpDir != "" {
		os.RemoveAll(tmpDir)
	}
}

// ─── steps ──────────────────────────────────────────────────────────

func stepInit() bool {
	var err error
	tmpDir, err = os.MkdirTemp("", "ipfs-integration-*")
	if err != nil {
		fmt.Printf("  FAIL: mkdir temp: %v\n", err)
		return false
	}
	repoDir = filepath.Join(tmpDir, "repo")

	fmt.Printf("  IPFS_PATH=%s\n", repoDir)

	// Init
	if _, err := runIPFS("init"); err != nil {
		fmt.Printf("  FAIL: ipfs init: %v\n", err)
		return false
	}
	fmt.Println("  OK: ipfs init")

	// Configure ports
	if _, err := runIPFS("config", "Addresses.Gateway", "/ip4/127.0.0.1/tcp/"+gatewayPort); err != nil {
		fmt.Printf("  FAIL: config gateway: %v\n", err)
		return false
	}
	if _, err := runIPFS("config", "Addresses.API", "/ip4/127.0.0.1/tcp/"+apiPort); err != nil {
		fmt.Printf("  FAIL: config api: %v\n", err)
		return false
	}
	// Disable DHT/bootstrap to keep things fast and local
	runIPFS("config", "Routing.Type", "none")

	fmt.Println("  OK: configured")
	return true
}

func stepStartDaemon() bool {
	daemonCmd = exec.Command("ipfs", "daemon", "--offline")
	daemonCmd.Env = append(os.Environ(), "IPFS_PATH="+repoDir)

	var stderr bytes.Buffer
	daemonCmd.Stderr = &stderr
	daemonCmd.Stdout = &stderr

	if err := daemonCmd.Start(); err != nil {
		fmt.Printf("  FAIL: start daemon: %v\n", err)
		return false
	}

	// Wait for daemon to be ready
	fmt.Print("  waiting for daemon...")
	ready := false
	for i := 0; i < 60; i++ {
		time.Sleep(500 * time.Millisecond)
		// Check via API
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%s/api/v0/id", apiPort))
		if err == nil {
			resp.Body.Close()
			ready = true
			break
		}
		fmt.Print(".")
	}
	fmt.Println()

	if !ready {
		fmt.Printf("  FAIL: daemon not ready after 30s\n")
		fmt.Printf("  stderr: %s\n", stderr.String())
		return false
	}

	// Verify gateway is responding
	fmt.Print("  checking gateway...")
	if !checkGateway() {
		fmt.Println(" FAIL")
		fmt.Println("  WARNING: gateway not responding, tests may fail")
	} else {
		fmt.Println(" OK")
	}

	return true
}

func stepAddFile() bool {
	testFile = filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, testData, 0644); err != nil {
		fmt.Printf("  FAIL: write test file: %v\n", err)
		return false
	}

	out, err := runIPFS("add", "-Q", testFile)
	if err != nil {
		fmt.Printf("  FAIL: ipfs add: %v\n", err)
		return false
	}
	testCID = out
	fmt.Printf("  OK: CID=%s (%d bytes)\n", testCID, len(testData))
	return true
}

func stepFetchByCID() bool {
	p := provider.NewIPFSProvider([]string{gatewayURL})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	data, err := p.FetchByCID(ctx, testCID)
	if err != nil {
		fmt.Printf("  FAIL: FetchByCID: %v\n", err)
		return false
	}

	if !bytes.Equal(data, testData) {
		fmt.Printf("  FAIL: content mismatch\n  expected: %q\n  got:      %q\n", testData, data)
		return false
	}

	fmt.Printf("  OK: fetched %d bytes, content matches\n", len(data))
	return true
}

func stepGetReader() bool {
	p := provider.NewIPFSProvider([]string{gatewayURL})

	reader, err := p.GetReader(testCID)
	if err != nil {
		fmt.Printf("  FAIL: GetReader: %v\n", err)
		return false
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		fmt.Printf("  FAIL: read body: %v\n", err)
		return false
	}

	if !bytes.Equal(data, testData) {
		fmt.Printf("  FAIL: content mismatch\n  expected: %q\n  got:      %q\n", testData, data)
		return false
	}

	fmt.Printf("  OK: read %d bytes, content matches\n", len(data))
	return true
}

func stepRaceGateways() bool {
	// Local gateway (good) + a non-existent gateway (bad)
	p := provider.NewIPFSProvider([]string{
		"http://127.0.0.1:19999", // no server here
		gatewayURL,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()
	data, err := p.FetchByCID(ctx, testCID)
	elapsed := time.Since(start)

	if err != nil {
		fmt.Printf("  FAIL: FetchByCID (race): %v\n", err)
		return false
	}

	if !bytes.Equal(data, testData) {
		fmt.Printf("  FAIL: content mismatch in race\n")
		return false
	}

	fmt.Printf("  OK: fetched via race in %v (good gateway won)\n", elapsed.Round(time.Millisecond))
	return true
}

func stepContextTimeout() bool {
	p := provider.NewIPFSProvider([]string{"http://127.0.0.1:19999"}) // no server

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := p.FetchByCID(ctx, testCID)
	if err == nil {
		fmt.Println("  FAIL: expected timeout error")
		return false
	}

	fmt.Printf("  OK: correctly errored on timeout: %v\n", err)
	return true
}

func stepNotFound() bool {
	p := provider.NewIPFSProvider([]string{gatewayURL})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := p.FetchByCID(ctx, "QmThisDoesNotExist12345")
	if err == nil {
		fmt.Println("  FAIL: expected error for non-existent CID")
		return false
	}

	fmt.Printf("  OK: correctly errored: %v\n", err)
	return true
}
