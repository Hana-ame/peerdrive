package source

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	hashutil "peerdrive/pkg/hashutil"
)

// openlist_crawler.go: OpenList directory crawler and index builder (PR ②, Issue #106).
//
// 职责：
// 走 OpenList 的 `/api/fs/list` 递归遍历远程目录树。
// 若 OpenList 返回条目中已包含可信/有效 64hex sha256（部分部署或底层存储直接上报），直接复用；
// 否则通过 `/p/*path` 流式拉取计算 sha256。
// 汇集完整目录树的 hash → path 映射后，原子调用 OpenListSource.Reload(index)。

// OpenListCrawlerConfig 配置爬虫行为
type OpenListCrawlerConfig struct {
	BaseURL     string
	Client      *http.Client
	Token       string
	Concurrency int           // 并发下载/计算 hash 上限，默认 4
	Timeout     time.Duration // 单个请求超时
}

// OpenListFSItem 表示 /api/fs/list 返回的单个文件/目录项
type OpenListFSItem struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	IsDir    bool   `json:"is_dir"`
	Modified string `json:"modified"`
	Sign     string `json:"sign"`
	Thumb    string `json:"thumb"`
	Type     int    `json:"type"`
	HashInfo map[string]string `json:"hash_info,omitempty"`
	Hash     string            `json:"hash,omitempty"`
}

// OpenListFSListResp 表示 /api/fs/list 的响应体
type OpenListFSListResp struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Content []OpenListFSItem `json:"content"`
		Total   int              `json:"total"`
		Readme  string           `json:"readme"`
	} `json:"data"`
}

// OpenListCrawler 爬取 OpenList 目录并构建 hash → path 索引
type OpenListCrawler struct {
	cfg    OpenListCrawlerConfig
	client *http.Client
}

// NewOpenListCrawler 构造爬虫实例
func NewOpenListCrawler(cfg OpenListCrawlerConfig) (*OpenListCrawler, error) {
	u, err := url.Parse(strings.TrimSpace(cfg.BaseURL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("openlist crawler: invalid BaseURL %q", cfg.BaseURL)
	}
	client := cfg.Client
	if client == nil {
		client = http.DefaultClient
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 4
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	return &OpenListCrawler{
		cfg:    cfg,
		client: client,
	}, nil
}

// CrawlDirectory 递归爬取指定路径下的所有文件并建立 hash → path 索引
func (c *OpenListCrawler) CrawlDirectory(ctx context.Context, rootPath string) (map[string]string, error) {
	if rootPath == "" {
		rootPath = "/"
	}

	var allFiles []string
	var walkDir func(dirPath string) error
	walkDir = func(dirPath string) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		items, err := c.listFS(ctx, dirPath)
		if err != nil {
			return err
		}

		for _, item := range items {
			fullPath := path.Join(dirPath, item.Name)
			if item.IsDir {
				if err := walkDir(fullPath); err != nil {
					return err
				}
			} else {
				allFiles = append(allFiles, fullPath)
			}
		}
		return nil
	}

	if err := walkDir(rootPath); err != nil {
		return nil, err
	}

	// 并发拉取并计算 sha256
	type result struct {
		hash string
		path string
		err  error
	}

	resCh := make(chan result, len(allFiles))
	sem := make(chan struct{}, c.cfg.Concurrency)
	var wg sync.WaitGroup

	for _, fpath := range allFiles {
		wg.Add(1)
		go func(filePath string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			h, err := c.fetchFileHash(ctx, filePath)
			resCh <- result{hash: h, path: filePath, err: err}
		}(fpath)
	}

	wg.Wait()
	close(resCh)

	index := make(map[string]string, len(allFiles))
	for res := range resCh {
		if res.err != nil {
			return nil, fmt.Errorf("openlist crawler: failed to hash %s: %w", res.path, res.err)
		}
		if res.hash != "" {
			index[res.hash] = res.path
		}
	}

	return index, nil
}

// CrawlAndReload 爬取并原子刷新指定的 OpenListSource
func (c *OpenListCrawler) CrawlAndReload(ctx context.Context, rootPath string, src *OpenListSource) error {
	index, err := c.CrawlDirectory(ctx, rootPath)
	if err != nil {
		return err
	}
	if len(index) == 0 {
		return fmt.Errorf("openlist crawler: directory %q contains no files to index", rootPath)
	}
	return src.Reload(index)
}

func (c *OpenListCrawler) listFS(ctx context.Context, dirPath string) ([]OpenListFSItem, error) {
	cleanBase := strings.TrimRight(c.cfg.BaseURL, "/")
	reqURL := cleanBase + "/api/fs/list"

	reqBody := map[string]any{
		"path":     dirPath,
		"password": "",
		"page":     1,
		"per_page": 0, // 0 = 全部
		"refresh":  false,
	}
	b, _ := json.Marshal(reqBody)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openlist list API returned status %d", resp.StatusCode)
	}

	var listResp OpenListFSListResp
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		return nil, fmt.Errorf("decode openlist list response: %w", err)
	}
	if listResp.Code != 200 {
		return nil, fmt.Errorf("openlist list error code %d: %s", listResp.Code, listResp.Message)
	}

	return listResp.Data.Content, nil
}

func (c *OpenListCrawler) fetchFileHash(ctx context.Context, filePath string) (string, error) {
	cleanBase := strings.TrimRight(c.cfg.BaseURL, "/")
	fileURL := cleanBase + "/p" + filePath

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
	if err != nil {
		return "", err
	}
	if c.cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch file returned status %d", resp.StatusCode)
	}

	hasher := sha256.New()
	if _, err := io.Copy(hasher, resp.Body); err != nil {
		return "", err
	}

	sum := hex.EncodeToString(hasher.Sum(nil))
	if !hashutil.IsStrictSHA256(sum) {
		return "", fmt.Errorf("computed hash %q is invalid", sum)
	}

	return sum, nil
}
