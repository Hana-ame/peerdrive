// p2p-test — 独立 P2P 协议测试工具。
//
// 仅依赖裸 go-libp2p，不导入 peerdrive 内部包。
// 直接操作 libp2p stream 层，测试 P2P 协议。
//
// 用法:
//   p2p-test --peer <multiaddr> --op <list|query|exists|get|size> [flags]
package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/multiformats/go-multiaddr"
)

const (
	protocolCollectionsList = "/peerdrive/collections/list/1.0.0"
	protocolExchange        = "/peerdrive/exchange/1.0.0"
)

// --- JSON 响应结构（与 peerdrive 服务端对齐）---

type collectionListResponse struct {
	UserCollections []userCollection `json:"user_collections"`
	AnonCollections []anonCollection `json:"anon_collections"`
}

type userCollection struct {
	ID              int      `json:"id"`
	Username        string   `json:"username"`
	CollectionName  string   `json:"collection_name"`
	CurrentHash     *string  `json:"current_hash"`
	Visibility      string   `json:"visibility"`
	FollowRedirects bool     `json:"follow_redirects"`
	Tags            []string `json:"tags"`
	CreatedAt       string   `json:"created_at"`
}

type anonCollection struct {
	Hash         string   `json:"hash"`
	FriendlyName string   `json:"friendly_name,omitempty"`
	NamePreview  string   `json:"name_preview,omitempty"`
	Version      int      `json:"version"`
	Tags         []string `json:"tags,omitempty"`
	EntryCount   int      `json:"entry_count"`
	CreatedAt    string   `json:"created_at"`
}

// --- 协议请求函数 ---

func requestCollectionList(ctx context.Context, h host.Host, peerID peer.ID, query string) ([]byte, error) {
	stream, err := h.NewStream(
		network.WithDialPeerTimeout(ctx, 30*time.Second),
		peerID,
		protocol.ID(protocolCollectionsList),
	)
	if err != nil {
		return nil, fmt.Errorf("打开 stream: %w", err)
	}
	defer stream.Close()

	stream.SetWriteDeadline(time.Now().Add(30 * time.Second))
	if _, err := fmt.Fprintf(stream, "%s\n", query); err != nil {
		return nil, fmt.Errorf("发送请求: %w", err)
	}

	stream.SetReadDeadline(time.Now().Add(30 * time.Second))
	reader := bufio.NewReader(stream)
	statusLine, err := reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("读取状态行: %w", err)
	}
	statusLine = strings.TrimSpace(statusLine)

	if strings.HasPrefix(statusLine, "ERR ") {
		return nil, fmt.Errorf("对端返回错误: %s", strings.TrimPrefix(statusLine, "ERR "))
	}

	var size int
	if _, scanErr := fmt.Sscanf(statusLine, "OK %d", &size); scanErr != nil || size <= 0 {
		return nil, fmt.Errorf("对端返回异常状态: %s", statusLine)
	}

	data := make([]byte, size)
	if _, err := io.ReadFull(reader, data); err != nil {
		return nil, fmt.Errorf("读取数据: %w", err)
	}

	return data, nil
}

func requestFile(ctx context.Context, h host.Host, peerID peer.ID, hash string) ([]byte, error) {
	stream, err := h.NewStream(
		network.WithDialPeerTimeout(ctx, 120*time.Second),
		peerID,
		protocol.ID(protocolExchange),
	)
	if err != nil {
		return nil, fmt.Errorf("打开 stream: %w", err)
	}
	defer stream.Close()

	stream.SetWriteDeadline(time.Now().Add(120 * time.Second))
	if _, err := fmt.Fprintf(stream, "%s\n", hash); err != nil {
		return nil, fmt.Errorf("发送请求: %w", err)
	}

	stream.SetReadDeadline(time.Now().Add(120 * time.Second))
	reader := bufio.NewReader(stream)
	statusLine, err := reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("读取状态行: %w", err)
	}
	statusLine = strings.TrimSpace(statusLine)

	if strings.HasPrefix(statusLine, "ERR ") {
		return nil, fmt.Errorf("对端返回错误: %s", strings.TrimPrefix(statusLine, "ERR "))
	}

	var size int
	if _, scanErr := fmt.Sscanf(statusLine, "OK %d", &size); scanErr != nil || size <= 0 {
		return nil, fmt.Errorf("对端返回异常状态: %s", statusLine)
	}

	data := make([]byte, size)
	if _, err := io.ReadFull(reader, data); err != nil {
		return nil, fmt.Errorf("读取数据: %w", err)
	}

	return data, nil
}

func requestFileSize(ctx context.Context, h host.Host, peerID peer.ID, hash string) (int, error) {
	stream, err := h.NewStream(
		network.WithDialPeerTimeout(ctx, 30*time.Second),
		peerID,
		protocol.ID(protocolExchange),
	)
	if err != nil {
		return 0, fmt.Errorf("打开 stream: %w", err)
	}
	defer stream.Close()

	stream.SetWriteDeadline(time.Now().Add(30 * time.Second))
	if _, err := fmt.Fprintf(stream, "SIZE %s\n", hash); err != nil {
		return 0, fmt.Errorf("发送请求: %w", err)
	}

	stream.SetReadDeadline(time.Now().Add(30 * time.Second))
	reader := bufio.NewReader(stream)
	statusLine, err := reader.ReadString('\n')
	if err != nil {
		return 0, fmt.Errorf("读取状态行: %w", err)
	}
	statusLine = strings.TrimSpace(statusLine)

	if strings.HasPrefix(statusLine, "ERR ") {
		return 0, fmt.Errorf("对端返回错误: %s", strings.TrimPrefix(statusLine, "ERR "))
	}

	var size int
	if _, scanErr := fmt.Sscanf(statusLine, "OK %d", &size); scanErr != nil || size < 0 {
		return 0, fmt.Errorf("对端返回异常状态: %s", statusLine)
	}

	return size, nil
}

// --- 操作函数 ---

func runList(ctx context.Context, h host.Host, peerID peer.ID) {
	fmt.Println("=== 列出所有公开 Collections ===")
	data, err := requestCollectionList(ctx, h, peerID, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "失败: %v\n", err)
		os.Exit(1)
	}

	var resp collectionListResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		fmt.Fprintf(os.Stderr, "失败: JSON 解析错误: %v\nraw: %s\n", err, string(data))
		os.Exit(1)
	}

	fmt.Printf("用户 Collections: %d\n", len(resp.UserCollections))
	for _, c := range resp.UserCollections {
		h := "<nil>"
		if c.CurrentHash != nil {
			h = *c.CurrentHash
		}
		fmt.Printf("  - %s/%s (visibility=%s, hash=%s)\n", c.Username, c.CollectionName, c.Visibility, shorten(h))
	}
	fmt.Printf("匿名 Collections: %d\n", len(resp.AnonCollections))
	for _, c := range resp.AnonCollections {
		fmt.Printf("  - %s (name=%q, entries=%d)\n", shorten(c.Hash), c.FriendlyName, c.EntryCount)
	}
	fmt.Println("\n✅ 完成")
}

func runQuery(ctx context.Context, h host.Host, peerID peer.ID, query string) {
	fmt.Printf("=== 查询 Collections (query=%q) ===\n", query)
	data, err := requestCollectionList(ctx, h, peerID, query)
	if err != nil {
		fmt.Fprintf(os.Stderr, "失败: %v\n", err)
		os.Exit(1)
	}

	var resp collectionListResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		fmt.Fprintf(os.Stderr, "失败: JSON 解析错误: %v\nraw: %s\n", err, string(data))
		os.Exit(1)
	}

	total := len(resp.UserCollections) + len(resp.AnonCollections)
	fmt.Printf("命中 %d 个 Collection:\n", total)
	for _, c := range resp.UserCollections {
		fmt.Printf("  [用户] %s/%s (visibility=%s)\n", c.Username, c.CollectionName, c.Visibility)
	}
	for _, c := range resp.AnonCollections {
		fmt.Printf("  [匿名] %s (name=%q)\n", shorten(c.Hash), c.FriendlyName)
	}
	fmt.Println("\n✅ query 完成")
}

func runExists(ctx context.Context, h host.Host, peerID peer.ID, query string) {
	fmt.Printf("=== 检查 Collection 是否存在 (query=%q) ===\n", query)
	if query == "" {
		fmt.Fprintln(os.Stderr, "错误: --query 不能为空")
		os.Exit(1)
	}

	data, err := requestCollectionList(ctx, h, peerID, query)
	if err != nil {
		fmt.Fprintf(os.Stderr, "失败: %v\n", err)
		os.Exit(1)
	}

	var resp collectionListResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		fmt.Fprintf(os.Stderr, "失败: JSON 解析错误: %v\nraw: %s\n", err, string(data))
		os.Exit(1)
	}

	total := len(resp.UserCollections) + len(resp.AnonCollections)
	if total > 0 {
		fmt.Printf("✅ 存在 (命中 %d 个)\n", total)
	} else {
		fmt.Println("❌ 不存在")
	}
}

func runGet(ctx context.Context, h host.Host, peerID peer.ID, hash, outPath string) {
	if len(hash) != 64 {
		fmt.Fprintf(os.Stderr, "错误: hash 必须为 64 位 hex (当前 %d 位)\n", len(hash))
		os.Exit(1)
	}
	fmt.Printf("=== 获取文件 (hash=%s) ===\n", shorten(hash))

	data, err := requestFile(ctx, h, peerID, hash)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			fmt.Printf("❌ 文件不存在 (hash=%s)\n", hash)
		} else {
			fmt.Fprintf(os.Stderr, "失败: %v\n", err)
		}
		os.Exit(1)
	}

	actual := fmt.Sprintf("%x", sha256.Sum256(data))
	if actual != hash {
		fmt.Fprintf(os.Stderr, "⚠️  SHA256 不匹配! 期望 %s, 实际 %s\n", hash, actual)
	}

	fmt.Printf("大小: %d 字节\n", len(data))
	fmt.Printf("SHA256: %s\n", actual)
	if actual == hash {
		fmt.Println("✅ SHA256 验证通过")
	}

	if outPath != "" {
		if err := os.WriteFile(outPath, data, 0644); err != nil {
			fmt.Fprintf(os.Stderr, "错误: 写入文件失败: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("已保存: %s\n", outPath)
	} else {
		preview := len(data)
		if preview > 256 {
			preview = 256
		}
		fmt.Printf("内容预览 (前 %d 字节): %x\n", preview, data[:preview])
		if len(data) > 256 {
			fmt.Printf("  ... 剩余 %d 字节省略\n", len(data)-256)
		}
	}
	fmt.Println("\n✅ get 完成")
}

func runSize(ctx context.Context, h host.Host, peerID peer.ID, hash string) {
	if len(hash) != 64 {
		fmt.Fprintf(os.Stderr, "错误: hash 必须为 64 位 hex (当前 %d 位)\n", len(hash))
		os.Exit(1)
	}
	fmt.Printf("=== 查询文件大小 (hash=%s) ===\n", shorten(hash))

	size, err := requestFileSize(ctx, h, peerID, hash)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			fmt.Printf("❌ 文件不存在 (hash=%s)\n", hash)
		} else {
			fmt.Fprintf(os.Stderr, "失败: %v\n", err)
		}
		os.Exit(1)
	}

	fmt.Printf("文件大小: %d 字节", size)
	if size > 1024*1024 {
		fmt.Printf(" (%.2f MB)", float64(size)/(1024*1024))
	} else if size > 1024 {
		fmt.Printf(" (%.2f KB)", float64(size)/1024)
	}
	fmt.Println()
	fmt.Println("\n✅ size 完成")
}

// --- 辅助函数 ---

func extractPeer(maddr multiaddr.Multiaddr) (peer.ID, multiaddr.Multiaddr) {
	peerIDStr, _ := maddr.ValueForProtocol(multiaddr.P_P2P)
	peerID, _ := peer.Decode(peerIDStr)

	transportAddr := maddr.Decapsulate(
		multiaddr.StringCast("/p2p/" + peerIDStr),
	)

	return peerID, transportAddr
}

func shorten(s string) string {
	if len(s) <= 16 {
		return s
	}
	return s[:8] + "..." + s[len(s)-8:]
}

// --- 入口 ---

func main() {
	peerAddr := flag.String("peer", "", "目标 peer multiaddr (必填)")
	op := flag.String("op", "list", "操作: list / query / exists / get / size")
	query := flag.String("query", "", "查询字符串 (用于 query/exists)")
	hash := flag.String("hash", "", "SHA256 hex (用于 get/size)")
	outPath := flag.String("out", "", "下载输出路径 (用于 get)")
	timeoutSec := flag.Int("timeout", 30, "操作超时秒数")
	flag.Parse()

	if *peerAddr == "" {
		fmt.Fprintln(os.Stderr, "错误: --peer 必填")
		flag.Usage()
		os.Exit(1)
	}

	ctxTimeout := time.Duration(*timeoutSec) * time.Second

	maddr, err := multiaddr.NewMultiaddr(*peerAddr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: 解析 multiaddr 失败: %v\n", err)
		os.Exit(1)
	}

	peerID, transportAddr := extractPeer(maddr)

	fmt.Printf("目标 Peer: %s\n", peerID)
	fmt.Printf("传输地址: %s\n", transportAddr)
	fmt.Println()

	// 创建裸 libp2p host（无 DHT、无 Relay、无 mDNS）
	h, err := libp2p.New(
		libp2p.ListenAddrStrings("/ip4/0.0.0.0/tcp/0"),
		libp2p.EnableAutoNATv2(),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: 创建 host 失败: %v\n", err)
		os.Exit(1)
	}
	defer h.Close()

	fmt.Printf("本地 Peer ID: %s\n", h.ID())
	fmt.Printf("本地地址: %v\n", h.Addrs())
	fmt.Println()

	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	dialCtx := network.WithDialPeerTimeout(ctx, ctxTimeout)

	// 连接目标 peer
	info := peer.AddrInfo{ID: peerID, Addrs: []multiaddr.Multiaddr{transportAddr}}
	if err := h.Connect(dialCtx, info); err != nil {
		fmt.Fprintf(os.Stderr, "错误: 连接失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("已连接: %s\n\n", peerID)

	// 执行操作
	switch *op {
	case "list":
		runList(ctx, h, peerID)
	case "query":
		runQuery(ctx, h, peerID, *query)
	case "exists":
		runExists(ctx, h, peerID, *query)
	case "get":
		runGet(ctx, h, peerID, *hash, *outPath)
	case "size":
		runSize(ctx, h, peerID, *hash)
	default:
		fmt.Fprintf(os.Stderr, "错误: 未知操作 %q\n", *op)
		os.Exit(1)
	}
}
