// Package mcp implements the Model Context Protocol (MCP) server over stdio for Peerdrive.
//
// 发现背景：Issue #81。AI 客户端（如 Claude Desktop, Cursor 等）主流通过 stdio
// 启动子进程与本地服务通信。通过提供轻量级、零外部依赖的 MCP stdio 服务器，
// 向客户端安全暴露只读能力（如本地文件搜索、合集元数据查询等）。
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"peerdrive/internal/config"
	"peerdrive/internal/repository"
	"peerdrive/internal/service"
)

// JSONRPCRequest represents an incoming JSON-RPC 2.0 message.
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// JSONRPCResponse represents an outgoing JSON-RPC 2.0 response.
type JSONRPCResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id,omitempty"`
	Result  any    `json:"result,omitempty"`
	Error   *RPCError `json:"error,omitempty"`
}

// RPCError represents a JSON-RPC error.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// ToolDefinition defines an MCP tool.
type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// Server handles MCP JSON-RPC messages via stdio.
type Server struct {
	cfg     *config.Config
	anonSvc *service.AnonService
}

// NewServer creates a new MCP stdio server.
func NewServer(cfg *config.Config) *Server {
	return &Server{
		cfg:     cfg,
		anonSvc: service.NewAnonService(cfg),
	}
}

// Run starts the read-evaluate-print loop on the provided reader and writer.
func (s *Server) Run(ctx context.Context, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	// Allow larger payloads (e.g., large collections) up to 10MB
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 10*1024*1024)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var req JSONRPCRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			s.sendError(out, nil, -32700, "Parse error", err.Error())
			continue
		}

		resp := s.handleRequest(ctx, req)
		if resp != nil {
			data, err := json.Marshal(resp)
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(out, "%s\n", data)
		}
	}

	return scanner.Err()
}

func (s *Server) sendError(out io.Writer, id any, code int, msg string, data any) {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &RPCError{
			Code:    code,
			Message: msg,
			Data:    data,
		},
	}
	b, _ := json.Marshal(resp)
	_, _ = fmt.Fprintf(out, "%s\n", b)
}

func (s *Server) handleRequest(ctx context.Context, req JSONRPCRequest) *JSONRPCResponse {
	// Notifications (without ID) generally do not receive replies unless errors occur.
	switch req.Method {
	case "initialize":
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"protocolVersion": "2024-11-05",
				"serverInfo": map[string]any{
					"name":    "peerdrive-mcp",
					"version": "0.2.0",
				},
				"capabilities": map[string]any{
					"tools": map[string]any{},
				},
			},
		}

	case "notifications/initialized":
		// Client ACK, no response required
		return nil

	case "ping":
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]any{},
		}

	case "tools/list":
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"tools": s.listTools(),
			},
		}

	case "tools/call":
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return &JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &RPCError{Code: -32602, Message: "Invalid params", Data: err.Error()},
			}
		}

		res, err := s.callTool(params.Name, params.Arguments)
		if err != nil {
			return &JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"content": []map[string]any{
						{
							"type": "text",
							"text": fmt.Sprintf("Error: %v", err),
						},
					},
					"isError": true,
				},
			}
		}

		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"content": []map[string]any{
					{
						"type": "text",
						"text": res,
					},
				},
			},
		}

	default:
		if req.ID == nil {
			return nil
		}
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &RPCError{Code: -32601, Message: fmt.Sprintf("Method not found: %s", req.Method)},
		}
	}
}

func (s *Server) listTools() []ToolDefinition {
	return []ToolDefinition{
		{
			Name:        "search_local_files",
			Description: "Search indexed files in local Peerdrive storage by filename/path query, size limits, and pagination.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "Substring to search in file name or path (case-insensitive ASCII)",
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Max results to return (default 100, clamped at 1000)",
					},
					"offset": map[string]any{
						"type":        "integer",
						"description": "Offset for pagination (default 0)",
					},
				},
			},
		},
		{
			Name:        "read_collection",
			Description: "Read metadata and file entries of a Peerdrive collection by its 64-hex SHA-256 hash.",
			InputSchema: map[string]any{
				"type": "object",
				"required": []string{"hash"},
				"properties": map[string]any{
					"hash": map[string]any{
						"type":        "string",
						"description": "64-character hex SHA-256 hash of the collection",
					},
				},
			},
		},
	}
}

func (s *Server) callTool(name string, args map[string]any) (string, error) {
	switch name {
	case "search_local_files":
		qStr, _ := args["query"].(string)
		limit := 100
		if l, ok := args["limit"].(float64); ok && l > 0 {
			limit = int(l)
		}
		offset := 0
		if o, ok := args["offset"].(float64); ok && o >= 0 {
			offset = int(o)
		}

		q := repository.SearchQuery{
			Q:      qStr,
			Limit:  limit,
			Offset: offset,
		}

		rows, total, err := repository.SearchFileIndex(q)
		if err != nil {
			return "", fmt.Errorf("search failed: %w", err)
		}

		type searchOutput struct {
			Total   int64                  `json:"total"`
			Count   int                    `json:"count"`
			Offset  int                    `json:"offset"`
			Results []repository.FileIndex `json:"results"`
		}

		out := searchOutput{
			Total:   total,
			Count:   len(rows),
			Offset:  offset,
			Results: rows,
		}

		b, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return "", err
		}
		return string(b), nil

	case "read_collection":
		hash, _ := args["hash"].(string)
		hash = strings.TrimSpace(hash)
		if hash == "" {
			return "", fmt.Errorf("hash argument is required")
		}

		coll, err := s.anonSvc.GetCollectionByHash(hash)
		if err != nil {
			return "", fmt.Errorf("failed to get collection: %w", err)
		}

		b, err := json.MarshalIndent(coll, "", "  ")
		if err != nil {
			return "", err
		}
		return string(b), nil

	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

// RunStdio initializes database and dependencies, then runs the MCP server on stdin/stdout.
func RunStdio() error {
	cfg := config.Load()
	if err := repository.InitDB(cfg.DBPath); err != nil {
		return fmt.Errorf("init db: %w", err)
	}
	defer repository.CloseDB()

	srv := NewServer(cfg)
	return srv.Run(context.Background(), os.Stdin, os.Stdout)
}
