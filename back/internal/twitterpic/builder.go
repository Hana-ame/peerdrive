package twitterpic

// builder.go: user → collection 生成器。
//
// 流程：拉用户 JSON（account_info + timeline）→ 按 URL 去重 → 逐条媒体：
//   - 构造条目元数据（path=tweets/<id>.<ext>、name、mime、created_at 取推文
//     date、modified_at 取同一值——API 没有独立 mtime，用推文时间保证内容
//     寻址的确定性：同内容重跑必得同 sha，不会因为"构建时刻"漂移）；
//   - 多备选 source：sha（摄取后回填）+ url（原始直链）+ ech-url（代理链）；
//   - 摄取（download → sha256 → collection.StoreFile 写 sha-文件系统）：
//     成功 → sha/size 回填，photo 的 preview = 自身 sha（该 API 无独立缩略图，
//     原图即预览，前端可经 CAS 渲染）；失败 → 条目保留远端备选（sha/preview
//     为空），集合仍可保存——「缺 preview」场景是格式允许的正常态。
//
// 数量上限：MaxFiles 只约束「摄取」条数（0=全部摄取）；timeline 全部条目都
// 进集合（条目廉价），超出上限的条目以纯远端备选存在，保证集合完整性。
// 分页：单用户 json.gz 是一次返回全量 timeline（前端 zip 下载直接遍历全量），
// 无分页；用户列表分页在 Client.ListUsers 处理。
//
// 写路径一律走 collection.StoreFile / pathutil（AGENTS 硬要求），不裸写文件。

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"peerdrive/internal/collection"
	"peerdrive/internal/log"
)

// BuildOptions 是构建一次 user→collection 的参数。
type BuildOptions struct {
	// Client 用于 API 与媒体下载（nil = http.DefaultClient）。
	Client *http.Client
	// StorageDir 是 sha-文件系统根（媒体与集合都存这里）。必填。
	StorageDir string
	// ProxyBase 是 ech-url 备选的代理基址（"" = 不填 ech-url 备选）。
	ProxyBase string
	// MaxFiles 是媒体摄取上限：0 = 全部摄取。
	MaxFiles int
	// MaxBytes 是单文件摄取上限（0 = 不限）；超过的媒体视为摄取失败，
	// 条目保留远端备选。
	MaxBytes int64
	// Monitor 记录每次媒体下载尝试（nil = 不记录）。
	Monitor *collection.FetchMonitor
}

// BuildResult 是一次构建的结果。
type BuildResult struct {
	Username      string
	CollectionSHA string // 集合 JSON 在 sha-文件系统里的地址
	Collection    *collection.Collection
	Ingested      int // 实际摄取（写入 sha-文件系统）的媒体数
}

// BuildUserCollection 拉取一个用户并生成其 collection，存进 sha-文件系统。
func BuildUserCollection(ctx context.Context, client *Client, username string, opt BuildOptions) (*BuildResult, error) {
	if opt.StorageDir == "" {
		return nil, fmt.Errorf("twitterpic: StorageDir is required")
	}
	meta, err := client.GetUser(ctx, username, "")
	if err != nil {
		return nil, err
	}
	if len(meta.Error) > 0 && string(meta.Error) != "null" {
		return nil, fmt.Errorf("twitterpic: user %q: server error: %s", username, string(meta.Error))
	}

	fetchOpt := collection.FetchOptions{Client: opt.Client, Monitor: opt.Monitor}
	seen := make(map[string]struct{}, len(meta.Timeline))
	entries := make([]collection.Entry, 0, len(meta.Timeline))
	ingested := 0

	for _, item := range meta.Timeline {
		if item.URL == "" {
			continue
		}
		if _, dup := seen[item.URL]; dup {
			continue // 与前端 timeline 去重一致：按 url 去重
		}
		seen[item.URL] = struct{}{}

		p, err := parseMediaURL(item.URL)
		if err != nil {
			log.LogWarn("twitterpic: %s: skip item: %v", username, err)
			continue
		}
		name := fileName(p.ID, p.Format, item.Type)
		created := parseItemDate(item.Date)
		src := &collection.SourceRef{URL: item.URL}
		if opt.ProxyBase != "" {
			src.ECHURL = proxyMediaURL(item.URL, opt.ProxyBase)
		}
		entry := collection.Entry{
			Path:       "tweets/" + name,
			Name:       name,
			MIME:       mimeFor(p.Format, item.Type),
			CreatedAt:  created,
			ModifiedAt: created,
			Source:     src,
		}

		// 摄取：按条目 Fetch 语义下载（ech-url 优先于 url，同一套备选源逻辑与
		// 监视），sha256 后写 sha-文件系统。
		if opt.MaxFiles == 0 || ingested < opt.MaxFiles {
			if data, alt, ok := ingestMedia(ctx, &entry, fetchOpt, opt.MaxBytes); ok {
				sha, err := collection.StoreFile(opt.StorageDir, data)
				if err != nil {
					// 写 CAS 失败：条目保持远端备选（与下载失败同一处理），
					// 不因单条失败废弃整个集合。
					log.LogWarn("twitterpic: %s: store media %q: %v", username, entry.Path, err)
				} else {
					entry.SHA = sha
					entry.Source.SHA = sha
					entry.Size = int64(len(data))
					if isImageType(item.Type) {
						// 该 API 无独立缩略图：photo 的 preview = 媒体自身 sha，
						// 前端可经 CAS 渲染预览而无需第二份文件。
						entry.Preview = sha
					}
					ingested++
					log.LogDebug("twitterpic: %s: ingested %s via %s (%d B)", username, entry.Path, alt, len(data))
				}
			}
		}
		entries = append(entries, entry)
	}

	coll, err := collection.New(entries)
	if err != nil {
		return nil, fmt.Errorf("twitterpic: build collection: %w", err)
	}
	sha, err := collection.Save(opt.StorageDir, coll)
	if err != nil {
		return nil, fmt.Errorf("twitterpic: save collection: %w", err)
	}
	return &BuildResult{
		Username:      username,
		CollectionSHA: sha,
		Collection:    coll,
		Ingested:      ingested,
	}, nil
}

// ingestMedia 按条目备选源语义下载媒体字节；ok=false 表示下载失败或超限
// （条目保持远端备选）。返回命中备选名。
func ingestMedia(ctx context.Context, entry *collection.Entry, opt collection.FetchOptions, maxBytes int64) (data []byte, alt string, ok bool) {
	rc, used, err := entry.Fetch(ctx, opt)
	if err != nil {
		log.LogWarn("twitterpic: ingest %q: %v", entry.Path, err)
		return nil, "", false
	}
	defer rc.Close()
	data, err = readBounded(rc, maxBytes)
	if err != nil {
		log.LogWarn("twitterpic: ingest %q: %v", entry.Path, err)
		return nil, "", false
	}
	return data, used, true
}

// readBounded 全量读取，超过 maxBytes（0=不限）报错而不是静默截断——截断会
// 产生错误内容的 sha，比失败更隐蔽。
func readBounded(rc io.Reader, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		return io.ReadAll(rc)
	}
	data, err := io.ReadAll(io.LimitReader(rc, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("twitterpic: media exceeds %d bytes", maxBytes)
	}
	return data, nil
}
