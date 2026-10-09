package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"peerdrive/internal/config"
	"peerdrive/internal/model"
	"peerdrive/internal/repository"
)

func TestMCPServer(t *testing.T) {
	// 发现背景：Issue #81 MCP server PR1 验收测试。
	// 验证 stdio JSON-RPC 2.0 循环：initialize、tools/list、以及 tools/call (search_local_files, read_collection)。
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "mcp_test.db")
	storageDir := filepath.Join(tmpDir, "storage")

	if err := repository.InitDB(dbPath); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() {
		_ = repository.CloseDB()
	})

	cfg := &config.Config{
		DBPath:     dbPath,
		StorageDir: storageDir,
	}

	// Insert test file index entry
	fileHash := strings.Repeat("a", 64)
	if _, err := repository.UpsertFileIndex(fileHash, filepath.Join(storageDir, "hello.txt"), "hello.txt", 100, false); err != nil {
		t.Fatalf("UpsertFileIndex: %v", err)
	}

	srv := NewServer(cfg)

	// 1. Test initialize
	initReq := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n"
	var outBuf bytes.Buffer
	if err := srv.Run(context.Background(), strings.NewReader(initReq), &outBuf); err != nil {
		t.Fatalf("Run init: %v", err)
	}

	var initResp JSONRPCResponse
	if err := json.Unmarshal(outBuf.Bytes(), &initResp); err != nil {
		t.Fatalf("Unmarshal initResp: %v (raw: %s)", err, outBuf.String())
	}
	if initResp.Error != nil {
		t.Fatalf("init returned error: %+v", initResp.Error)
	}

	// 2. Test tools/list
	listReq := `{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n"
	outBuf.Reset()
	if err := srv.Run(context.Background(), strings.NewReader(listReq), &outBuf); err != nil {
		t.Fatalf("Run list: %v", err)
	}

	var listResp struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Result  struct {
			Tools []ToolDefinition `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(outBuf.Bytes(), &listResp); err != nil {
		t.Fatalf("Unmarshal listResp: %v", err)
	}
	if len(listResp.Result.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(listResp.Result.Tools))
	}

	// 3. Test tools/call search_local_files
	callReq := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search_local_files","arguments":{"query":"hello"}}}` + "\n"
	outBuf.Reset()
	if err := srv.Run(context.Background(), strings.NewReader(callReq), &outBuf); err != nil {
		t.Fatalf("Run call search: %v", err)
	}

	var callResp struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Result  struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(outBuf.Bytes(), &callResp); err != nil {
		t.Fatalf("Unmarshal callResp: %v (raw: %s)", err, outBuf.String())
	}
	if callResp.Result.IsError {
		t.Fatalf("tools/call returned error content: %+v", callResp.Result.Content)
	}
	if len(callResp.Result.Content) == 0 || !strings.Contains(callResp.Result.Content[0].Text, "hello.txt") {
		t.Fatalf("search result did not contain hello.txt: %+v", callResp.Result.Content)
	}

	// 4. Test tools/call read_collection (create dummy collection first)
	coll := &model.AnonCollection{
		Version:     1,
		Name:        "test-collection",
		CreatedTime: 1234567890,
		Entries: []model.AnonCollectionEntry{
			{Name: "item1.txt", Hash: fileHash, Size: 100},
		},
	}
	collJSON, _ := json.Marshal(coll)
	collHash := sha256Hex(collJSON)
	collDir := filepath.Join(storageDir, collHash[:2])
	_ = os.MkdirAll(collDir, 0755)
	if err := os.WriteFile(filepath.Join(collDir, collHash), collJSON, 0644); err != nil {
		t.Fatalf("WriteFile coll: %v", err)
	}

	callCollReq := `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"read_collection","arguments":{"hash":"` + collHash + `"}}}` + "\n"
	outBuf.Reset()
	if err := srv.Run(context.Background(), strings.NewReader(callCollReq), &outBuf); err != nil {
		t.Fatalf("Run call read_collection: %v", err)
	}

	var callCollResp struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Result  struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(outBuf.Bytes(), &callCollResp); err != nil {
		t.Fatalf("Unmarshal callCollResp: %v", err)
	}
	if callCollResp.Result.IsError {
		t.Fatalf("read_collection returned error: %+v", callCollResp.Result.Content)
	}
	if len(callCollResp.Result.Content) == 0 || !strings.Contains(callCollResp.Result.Content[0].Text, "test-collection") {
		t.Fatalf("read_collection response missing test-collection: %+v", callCollResp.Result.Content)
	}
}
