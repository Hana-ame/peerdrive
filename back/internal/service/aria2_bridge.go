package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"peerdrive/internal/config"
	"peerdrive/internal/log"
)

// aria2_bridge.go — aria2c RPC client bridge & transfer lifecycle manager (Issue #236).
//
// Background: Issue #236 requires compatible integration with aria2c (ara2c) for
// high-speed multi-connection downloads, with dynamic toggle control and zero overhead
// when disabled.
//
// Architecture:
// 1. Aria2Bridge connects to aria2c's JSON-RPC endpoint (http://127.0.0.1:6800/jsonrpc).
// 2. Supports token authentication via "token:<secret>".
// 3. Translates download tasks into aria2.addUri calls, and polls tellStatus for progress.
// 4. Integrates seamlessly into Peerdrive's transfer monitoring surface.

// Aria2Status represents the status of an aria2 download task.
type Aria2Status struct {
	GID             string `json:"gid"`
	Status          string `json:"status"` // active, waiting, paused, error, complete, removed
	TotalLength     int64  `json:"totalLength,string"`
	CompletedLength int64  `json:"completedLength,string"`
	DownloadSpeed   int64  `json:"downloadSpeed,string"`
	ErrorCode       string `json:"errorCode,omitempty"`
	ErrorMessage    string `json:"errorMessage,omitempty"`
	Dir             string `json:"dir,omitempty"`
	Files           []struct {
		Path            string `json:"path"`
		Length          int64  `json:"length,string"`
		CompletedLength int64  `json:"completedLength,string"`
	} `json:"files,omitempty"`
}

// Aria2Bridge manages communication with the aria2c daemon.
type Aria2Bridge struct {
	mu         sync.RWMutex
	enabled    bool
	rpcURL     string
	secret     string
	targetDir  string
	httpClient *http.Client
}

// NewAria2Bridge creates a new aria2 bridge instance.
func NewAria2Bridge(cfg *config.Config) *Aria2Bridge {
	targetDir := cfg.Aria2DownloadDir
	if targetDir == "" {
		targetDir = cfg.DownloadDir
	}
	return &Aria2Bridge{
		enabled:   cfg.Aria2Enable,
		rpcURL:    cfg.Aria2RPCURL,
		secret:    cfg.Aria2RPCSecret,
		targetDir: targetDir,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// IsEnabled reports whether the aria2 bridge is enabled.
func (a *Aria2Bridge) IsEnabled() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.enabled
}

// SetEnabled toggles the aria2 bridge at runtime.
func (a *Aria2Bridge) SetEnabled(enabled bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.enabled = enabled
	log.LogInfo("aria2: enabled state set to %v", enabled)
}

// rpcReq represents a standard JSON-RPC 2.0 request payload.
type rpcReq struct {
	JSONRPC string `json:"jsonrpc"`
	ID      string `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

// rpcResp represents a standard JSON-RPC 2.0 response payload.
type rpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      string          `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// callRPC invokes an aria2 JSON-RPC method with secret token prepending.
func (a *Aria2Bridge) callRPC(ctx context.Context, method string, params ...any) ([]byte, error) {
	a.mu.RLock()
	if !a.enabled {
		a.mu.RUnlock()
		return nil, errors.New("aria2 integration is disabled")
	}
	rpcURL := a.rpcURL
	secret := a.secret
	a.mu.RUnlock()

	var actualParams []any
	if secret != "" {
		actualParams = append(actualParams, "token:"+secret)
	}
	actualParams = append(actualParams, params...)

	payload := rpcReq{
		JSONRPC: "2.0",
		ID:      fmt.Sprintf("pd-%d", time.Now().UnixNano()),
		Method:  method,
		Params:  actualParams,
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, rpcURL, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("aria2 rpc request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read aria2 rpc response: %w", err)
	}

	var rpcRes rpcResp
	if err := json.Unmarshal(body, &rpcRes); err != nil {
		return nil, fmt.Errorf("unmarshal aria2 rpc response: %w", err)
	}

	if rpcRes.Error != nil {
		return nil, fmt.Errorf("aria2 error %d: %s", rpcRes.Error.Code, rpcRes.Error.Message)
	}

	return rpcRes.Result, nil
}

// AddURI sends an HTTP/HTTPS/FTP URI to aria2 for download.
func (a *Aria2Bridge) AddURI(ctx context.Context, uri string, outFilename string) (string, error) {
	options := map[string]any{}
	a.mu.RLock()
	if a.targetDir != "" {
		options["dir"] = a.targetDir
	}
	a.mu.RUnlock()

	if outFilename != "" {
		options["out"] = outFilename
	}

	res, err := a.callRPC(ctx, "aria2.addUri", []string{uri}, options)
	if err != nil {
		return "", err
	}

	var gid string
	if err := json.Unmarshal(res, &gid); err != nil {
		return "", err
	}
	return gid, nil
}

// TellStatus queries the download status of an existing GID.
func (a *Aria2Bridge) TellStatus(ctx context.Context, gid string) (*Aria2Status, error) {
	res, err := a.callRPC(ctx, "aria2.tellStatus", gid, []string{
		"gid", "status", "totalLength", "completedLength", "downloadSpeed",
		"errorCode", "errorMessage", "dir", "files",
	})
	if err != nil {
		return nil, err
	}

	var status Aria2Status
	if err := json.Unmarshal(res, &status); err != nil {
		return nil, err
	}
	return &status, nil
}

// Pause pauses an active download.
func (a *Aria2Bridge) Pause(ctx context.Context, gid string) error {
	_, err := a.callRPC(ctx, "aria2.pause", gid)
	return err
}

// Resume unpauses a paused download.
func (a *Aria2Bridge) Resume(ctx context.Context, gid string) error {
	_, err := a.callRPC(ctx, "aria2.unpause", gid)
	return err
}

// Cancel removes a download task from aria2.
func (a *Aria2Bridge) Cancel(ctx context.Context, gid string) error {
	_, err := a.callRPC(ctx, "aria2.remove", gid)
	return err
}

// GetVersion verifies connection to the aria2 daemon.
func (a *Aria2Bridge) GetVersion(ctx context.Context) (string, error) {
	res, err := a.callRPC(ctx, "aria2.getVersion")
	if err != nil {
		return "", err
	}
	var ver struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(res, &ver); err != nil {
		return "", err
	}
	return ver.Version, nil
}
