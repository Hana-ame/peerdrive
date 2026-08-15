// IPFS 集成测试 — 启动临时 IPFS 节点，用 IPFSProvider 通过本地网关获取内容。
//
// 前置条件：系统已安装 ipfs (kubo) 命令行。
//
// 用法：
//
//	go run ./test/ipfs-integration/

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
	daemonCmd *exec.Cmd
	tmpDir    string
	repoDir   string
	exitCode  int
)

func main() {
	fmt.Println("==============================================")
	fmt.Println("  Peerdrive IPFS Gateway Integration Test")
	fmt.Println("==============================================")
	fmt.Println()

	defer func() {
		cleanup()
		os.Exit(exitCode)
	}()

	if !checkPrereqs() {
		exitCode = 1
		return
	}
	if !stepInit() {
		exitCode = 1
		return
	}
	if !stepStartDaemon() {
		exitCode = 1
		return
	}
	if !stepAddFile() {
		exitCode = 1
		return
	}

	allPass := true
	allPass = stepFetchByCID() && allPass
	allPass = stepGetReader() && allPass
	allPass = stepRaceGateways() && allPass
	allPass = stepContextTimeout() && allPass
	allPass = stepNotFound() && allPass

	fmt.Println()
	fmt.Println("==============================================")
	if allPass {
		fmt.Println("  ALL IPFS INTEGRATION TESTS PASSED")
	} else {
		fmt.Println("  SOME TESTS FAILED")
		exitCode = 1
	}
	fmt.Println("==============================================")
}

// ─── infrastructure ─────────────────────────────────────────────────

func checkPrereqs() bool {
	fmt.Println("--- Step 1: Check prerequisites ---")
	if _, err := exec.LookPath("ipfs"); err != nil {
		fmt.Printf("  FAIL: ipfs not found (%v)\n", err)
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
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%v: %s", err, stderr.String())
	}
	return strings.TrimSpace(stdout.String()), nil
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

func stepInit() bool {
	fmt.Println("\n--- Step 2: Init IPFS node ---")
	var err error
	tmpDir, err = os.MkdirTemp("", "ipfs-integration-*")
	if err != nil {
		fmt.Printf("  FAIL: mkdir temp: %v\n", err)
		return false
	}
	repoDir = filepath.Join(tmpDir, "repo")
	fmt.Printf("  IPFS_PATH=%s\n", repoDir)

	if _, err := runIPFS("init"); err != nil {
		fmt.Printf("  FAIL: ipfs init: %v\n", err)
		return false
	}
	runIPFS("config", "Addresses.Gateway", "/ip4/127.0.0.1/tcp/"+gatewayPort)
	runIPFS("config", "Addresses.API", "/ip4/127.0.0.1/tcp/"+apiPort)
	runIPFS("config", "Routing.Type", "none")
	fmt.Println("  OK")
	return true
}

func stepStartDaemon() bool {
	fmt.Println("\n--- Step 3: Start IPFS daemon ---")
	daemonCmd = exec.Command("ipfs", "daemon", "--offline")
	daemonCmd.Env = append(os.Environ(), "IPFS_PATH="+repoDir)
	var stderr bytes.Buffer
	daemonCmd.Stderr = &stderr
	daemonCmd.Stdout = &stderr

	if err := daemonCmd.Start(); err != nil {
		fmt.Printf("  FAIL: start daemon: %v\n", err)
		return false
	}

	fmt.Print("  waiting for daemon")
	ready := false
	for i := 0; i < 60; i++ {
		time.Sleep(500 * time.Millisecond)
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
		fmt.Printf("  FAIL: daemon not ready after 30s\n%s\n", stderr.String())
		return false
	}
	fmt.Println("  OK")
	return true
}

func stepAddFile() bool {
	fmt.Println("\n--- Step 4: Add test file ---")
	if err := os.WriteFile(filepath.Join(tmpDir, "test.txt"), testData, 0644); err != nil {
		fmt.Printf("  FAIL: %v\n", err)
		return false
	}
	out, err := runIPFS("add", "-Q", filepath.Join(tmpDir, "test.txt"))
	if err != nil {
		fmt.Printf("  FAIL: ipfs add: %v\n", err)
		return false
	}
	testCID = out
	fmt.Printf("  OK: CID=%s (%d bytes)\n", testCID, len(testData))
	return true
}

// ─── tests ──────────────────────────────────────────────────────────

func stepFetchByCID() bool {
	fmt.Println("\n--- Step 5: FetchByCID ---")
	p := provider.NewIPFSProvider([]string{gatewayURL})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	data, err := p.FetchByCID(ctx, testCID)
	if err != nil {
		fmt.Printf("  FAIL: %v\n", err)
		return false
	}
	if !bytes.Equal(data, testData) {
		fmt.Printf("  FAIL: content mismatch: got %d bytes, expected %d\n", len(data), len(testData))
		return false
	}
	fmt.Printf("  PASS: %d bytes, content matches\n", len(data))
	return true
}

func stepGetReader() bool {
	fmt.Println("\n--- Step 6: GetReader ---")
	p := provider.NewIPFSProvider([]string{gatewayURL})
	reader, err := p.GetReader(testCID)
	if err != nil {
		fmt.Printf("  FAIL: %v\n", err)
		return false
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		fmt.Printf("  FAIL: read body: %v\n", err)
		return false
	}
	if !bytes.Equal(data, testData) {
		fmt.Printf("  FAIL: content mismatch\n")
		return false
	}
	fmt.Printf("  PASS: %d bytes via ReadCloser\n", len(data))
	return true
}

func stepRaceGateways() bool {
	fmt.Println("\n--- Step 7: Race gateways ---")
	p := provider.NewIPFSProvider([]string{
		"http://127.0.0.1:19999", // dead
		gatewayURL,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()
	data, err := p.FetchByCID(ctx, testCID)
	elapsed := time.Since(start)
	if err != nil {
		fmt.Printf("  FAIL: %v\n", err)
		return false
	}
	if !bytes.Equal(data, testData) {
		fmt.Println("  FAIL: content mismatch")
		return false
	}
	fmt.Printf("  PASS: good gateway won in %v\n", elapsed.Round(time.Millisecond))
	return true
}

func stepContextTimeout() bool {
	fmt.Println("\n--- Step 8: Context timeout ---")
	p := provider.NewIPFSProvider([]string{"http://127.0.0.1:19999"})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := p.FetchByCID(ctx, testCID)
	if err == nil {
		fmt.Println("  FAIL: expected timeout error")
		return false
	}
	fmt.Printf("  PASS: %v\n", err)
	return true
}

func stepNotFound() bool {
	fmt.Println("\n--- Step 9: Non-existent CID ---")
	p := provider.NewIPFSProvider([]string{gatewayURL})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := p.FetchByCID(ctx, "QmThisDoesNotExist12345XX")
	if err == nil {
		fmt.Println("  FAIL: expected error for non-existent CID")
		return false
	}
	fmt.Printf("  PASS: %v\n", err)
	return true
}
