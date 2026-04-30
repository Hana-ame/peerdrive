// Package provider 定义可插拔的内容提供者接口 ContentProvider。
// 支持两种提供者类型：
//   "local" — 读取本地文件系统（通过 filepath.Join(BaseDir, path)）
//   "http"  — 通过 HTTP GET 获取远程资源
// Manager 根据 provider_type 字符串路由 GetReader 调用到对应提供者。
// 扩展方式：实现 ContentProvider 接口并在 Manager.providers map 中注册。

package provider

import (
	"io"
)

type ContentProvider interface {
	GetReader(path string) (io.ReadCloser, error)
	GetFilenameHint(path, originalFilename string) string
}
