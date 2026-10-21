package twitterpic

// media.go: twimg 媒体 URL 的解析与派生（id / 文件名 / mime / 代理 URL）。
//
// 数据面事实（前端源码确认）：timeline 条目的 url 是原始 twimg 直链，形如
//
//	https://pbs.twimg.com/media/HQQFyW8aIAAwogx?format=jpg&name=medium
//
// 媒体 id 是路径最后一段（HQQFyW8aIAAwogx），扩展名取 query 的 format 参数；
// 图片代理 = 固定源 https://pbs.moonchan.xyz + 相对路径（前端 extractMediaPath
// 剥掉 pbs.twimg.com 的 origin，保留 path+query）。文件名规则镜像前端
// extractFileName：有 format 用 "<id>.<format>"，否则按类型 photo→jpg、
// video/animated_gif→mp4。

import (
	"fmt"
	"net/url"
	"path"
	"strings"
)

// mediaParts 是解析后的媒体 URL 要素。
type mediaParts struct {
	ID     string // 媒体 id（路径最后一段）
	Format string // query format 参数（"" = 未声明）
	Path   string // 相对路径（含 query），用于拼代理 URL
}

// parseMediaURL 解析一条 twimg 媒体直链。
func parseMediaURL(raw string) (*mediaParts, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("twitterpic: parse media url: %w", err)
	}
	if u.Host == "" || u.Path == "" {
		return nil, fmt.Errorf("twitterpic: media url %q has no host/path", raw)
	}
	id := path.Base(u.Path)
	if id == "" || id == "." || id == "/" {
		return nil, fmt.Errorf("twitterpic: media url %q has no media id", raw)
	}
	format := u.Query().Get("format")
	rel := u.Path
	if u.RawQuery != "" {
		rel += "?" + u.RawQuery
	}
	if u.Fragment != "" {
		rel += "#" + u.Fragment
	}
	return &mediaParts{ID: id, Format: format, Path: rel}, nil
}

// fileName 构造展示/下载默认文件名：<id>.<ext>，ext 缺省按类型
// photo→jpg、video/animated_gif→mp4（与前端 extractFileName 的兜底一致）。
func fileName(id, format, typ string) string {
	ext := format
	if ext == "" {
		switch typ {
		case "video", "animated_gif":
			ext = "mp4"
		default:
			ext = "jpg"
		}
	}
	return id + "." + ext
}

// mimeFor 由类型 + format 推导媒体类型。photo→image/<format>（jpg→jpeg），
// video/animated_gif→video/mp4；未知类型返回 ""（= 未知）。
func mimeFor(format, typ string) string {
	switch typ {
	case "video", "animated_gif":
		return "video/mp4"
	case "photo":
		switch format {
		case "jpg":
			return "image/jpeg"
		case "jpeg":
			return "image/jpeg"
		case "webp":
			return "image/webp"
		case "png":
			return "image/png"
		case "gif":
			return "image/gif"
		case "":
			return "image/jpeg" // 默认照片格式
		default:
			return "image/" + format
		}
	default:
		return ""
	}
}

// isImageType 判断类型是否图片（photo）。
func isImageType(typ string) bool { return typ == "photo" }

// proxyMediaURL 把原始 twimg 直链改成代理直链：proxyBase + 相对路径
// （镜像前端 extractMediaPath + FIXED_IMAGE_PROXY 的拼接）。
func proxyMediaURL(raw, proxyBase string) string {
	p, err := parseMediaURL(raw)
	if err != nil {
		return ""
	}
	return strings.TrimRight(proxyBase, "/") + p.Path
}
