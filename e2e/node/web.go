package node

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
)

// assets 把浏览器页面依赖的仓库内单文件挂到固定 URL。
//
// 刻意不拷贝副本：帧编解码（webrtc/frame.js）与 SHA-256（peerdrive-core）
// 在仓库里各有唯一实现，复制一份就等于让 e2e 验证的是副本、而不是真实实现。
var assets = map[string]string{
	"/assets/frame.js":      "webrtc/frame.js",
	"/assets/sha256.js":     "packages/peerdrive-core/src/sha256.js",
	"/assets/peerjs.min.js": "peerjs/peerjs.min.js",
}

// WebHandler 返回静态页 + 仓库内资源的路由；repo 为仓库根目录。
//
// 启动时先检查三个资源是否都在：否则浏览器要到运行时才 404，排查会绕很远。
func WebHandler(repo string) (http.Handler, error) {
	absRepo, err := filepath.Abs(repo)
	if err != nil {
		return nil, fmt.Errorf("e2e: 解析 repo 根目录: %w", err)
	}

	mux := http.NewServeMux()
	for urlPath, rel := range assets {
		abs := filepath.Join(absRepo, filepath.FromSlash(rel))
		if _, err := os.Stat(abs); err != nil {
			return nil, fmt.Errorf("e2e: 资源 %s 不存在（用 -repo 指定仓库根目录）: %w", rel, err)
		}
		file := abs
		mux.HandleFunc(urlPath, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			http.ServeFile(w, r, file)
		})
	}

	webDir := filepath.Join(absRepo, "e2e", "web")
	if _, err := os.Stat(filepath.Join(webDir, "index.html")); err != nil {
		return nil, fmt.Errorf("e2e: 页面 %s 不存在: %w", webDir, err)
	}
	mux.Handle("/", http.FileServer(http.Dir(webDir)))

	// 供 e2e 脚本判断「页面服务已就绪」，与 P2P 链路无关。
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte("ok"))
	})
	return mux, nil
}
