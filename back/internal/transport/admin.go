package transport

// admin.go：管理面 verb（浏览器经本地 WS 会话管理本节点）。
//
// 为什么只允许本地会话（c.ID()=="local"）：
//   - WebRTC/PeerJS 连接可能来自公共信令上的任意节点，若管理 verb 同样实现，
//     等于把本节点管理口（认证 token、文件读写、集合变更）开放给未知对端，
//     权限面完全暴露。管理面固定走本地 WS 会话，WebRTC 只承载文件数据面
//     （req/upload/index verb，见 conn.go bindConn 分派）。
//   - 浏览器经 /ws/peer 建立 WSSession（id="local"，Origin 白名单校验，见
//     peerjs_routes.go registerPeerJSRoutes），再发 admin 帧管理本节点。
//
// 内部转发设计：admin 帧 → 构造内部 *http.Request → 注入 gin engine 的
// ServeHTTP（router.SetupRouter 里经 SetAdminHandler 注入）→ 复用全部
// controller 逻辑（文件/集合/分享/任务/BT/IPFS 等零重复实现）。响应：
//   - JSON 响应 → {"type":"admin-resp","status":N,"body":<原始 JSON>,"reqId"}
//   - 二进制响应（文件流）→ {"type":"admin-bin","status":N,"size":N,"reqId"}
//     + 紧随一个二进制帧（整体发送；上限 adminBinMax 防 OOM）
//   - 失败 → {"type":"err","msg":"...","reqId"}
//
// 上传：admin 帧带 binary=true + filename/size，后续二进制帧作为请求体收集到
// 临时文件，收齐后构造 multipart/form-data 转发（controller 的 FormFile 读取
// 无感知）。与旧 upload verb（文件索引/下载目录会话）互斥独立——admin 上传
// 走 storageDir 内容寻址存储（/files/upload 语义）。
//
// 认证：前端把 getAuthToken() 的 token 放进 admin 帧 token 字段，转发时注入
// Authorization header → gin 的 AuthRequired 中间件行为与 HTTP 完全一致。

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"peerdrive/internal/log"
)

// adminBinMax 管理面二进制响应上限（64MB）：内部转发已把整个响应驻留内存
// （httptest recorder），大文件应走 req verb 分片拉取（前端 ws.js 下载路径）。
const adminBinMax = 64 << 20

// adminUploadTimeout 上传收集超时：浏览器声明 binary 上传后不发数据块会
// 永久占位（同 M6 对 upload verb 的修复）。超时中止并清理临时文件。
const adminUploadTimeout = 30 * time.Second

// AdminHandler 内部转发函数：把已构造好的内部请求交给 gin engine。
// 返回 HTTP 状态码、响应体、Content-Type。由 router 层注入。
type AdminHandler func(req *http.Request) (int, []byte, string, error)

// adminReq 管理面请求帧（浏览器 → 本地会话）。
// binary=true 时后续二进制帧作为请求体（multipart 上传）。
type adminReq struct {
	Type   string `json:"type"`
	Method string `json:"method"`
	Path   string `json:"path"` // 含 query string，如 "/files/upload"
	Body   any    `json:"body,omitempty"`
	Token  string `json:"token,omitempty"`

	// 二进制上传声明（binary=true）：filename 用于 multipart 文件名，
	// field 用于 multipart 字段名（默认 "file"；BT torrent 上传用 "torrent"）
	Binary   bool   `json:"binary,omitempty"`
	Filename string `json:"filename,omitempty"`
	Field    string `json:"field,omitempty"`
	Size     int64  `json:"size,omitempty"`

	ReqID string `json:"reqId,omitempty"`
}

// adminResp 管理面响应帧（本地会话 → 浏览器）。
// Body 为原始 JSON（gin 已序列化的响应体），前端按 reqId 路由后直接使用。
type adminResp struct {
	Type   string `json:"type"`
	Status int    `json:"status"`
	Body   any    `json:"body,omitempty"`
	ReqID  string `json:"reqId,omitempty"`
}

// ginH 轻量 JSON 对象（避免引入 gin 依赖）。
type ginH map[string]any

// adminBinResp 二进制响应头（JSON 文本帧，随后紧跟一个二进制数据帧）。
type adminBinResp struct {
	Type   string `json:"type"`
	Status int    `json:"status"`
	Size   int64  `json:"size"`
	ReqID  string `json:"reqId,omitempty"`
}

// adminUploadState 管理面二进制上传收集状态（连接级单槽，同 pendingUpload）。
type adminUploadState struct {
	reqID    string
	filename string
	field    string // multipart 字段名（默认 "file"；BT torrent 用 "torrent"）
	reqPath  string // 转发目标路径（默认 /files/upload；BT torrent 走 /bt/torrent）
	got      int64
	size     int64
	f        *os.File // 临时文件（body 收集），收齐后 multipart 转发
	path     string
	created  time.Time
	aborted  bool // 泵内中止标记（size 超限/超时）→ worker 清理回 err
}

// serveAdmin 处理管理面 admin 帧（仅本地会话；分派见 bindConn）。
// 注意：binary 上传声明帧的 adminUp 设置必须发生在消息泵内（同步），
// 否则泵已路由后续二进制帧时 adminUp 仍为空 → 数据块丢失（发现背景：
// 初版 case "admin" 全部 go 异步，上传分片先于 adminUp 到达，上传永远收不齐）。
func (s *PeerJSService) serveAdmin(c Session, st *connState, raw []byte) {
	if c.ID() != "local" {
		// 管理面不开放给 WebRTC 连接（文件头注释：防权限面暴露）
		c.SendJSON(dcResp{Type: "err", Msg: "admin verb is only allowed on the local session"})
		return
	}
	var ar adminReq
	if err := json.Unmarshal(raw, &ar); err != nil || ar.Method == "" || ar.Path == "" {
		c.SendJSON(dcResp{Type: "err", Msg: "invalid admin request"})
		return
	}
	if s.adminHandler == nil {
		c.SendJSON(dcResp{Type: "err", Msg: "admin handler not configured", ReqID: ar.ReqID})
		return
	}

	// 二进制上传：同步占槽（泵内），收集由二进制帧路由（conn.go）驱动，
	// 收齐后由泵触发 serveAdminUploadComplete
	if ar.Binary {
		if ar.Size <= 0 || ar.Size > adminBinMax {
			c.SendJSON(dcResp{Type: "err", Msg: "invalid admin upload size", ReqID: ar.ReqID})
			return
		}
		f, err := os.CreateTemp("", "peerdrive-admin-upload-*")
		if err != nil {
			c.SendJSON(dcResp{Type: "err", Msg: "upload temp file failed: " + err.Error(), ReqID: ar.ReqID})
			return
		}
		st.mu.Lock()
		// 防御：上一槽未收齐（浏览器放弃/异常）——直接替换并清旧文件。
		// 浏览器串行声明上传，不会出现两个并发声明；恶意重复声明只清临时文件。
		if old := st.adminUp; old != nil {
			os.Remove(old.path)
			old.f.Close()
		}
		au := &adminUploadState{
			reqID:    ar.ReqID,
			filename: ar.Filename,
			field:    ar.Field,
			// 转发目标路径：默认 /files/upload（存储语义），BT torrent 上传
			// 传 path=/bt/torrent。旧实现硬编码 /files/upload，导致 torrent
			// 上传打到 /files/upload 且 field=torrent 不被接受（发现背景：
			// 前端 btTorrentUpload 迁移时核对 admin 帧与后端转发路径）。
			reqPath: ar.Path,
			size:    ar.Size,
			f:       f,
			path:    f.Name(),
			created: time.Now(),
		}
		st.adminUp = au
		st.mu.Unlock()
		log.LogInfo("peerjs-admin: upload begin %s size=%d", ar.Filename, ar.Size)
		return // 收齐后由泵触发 serveAdminUploadComplete
	}

	// 普通请求：JSON body → 内部 HTTP 转发。
	// 异步 dispatch：内部 HTTP 转发可能较慢（大列表/慢客户端），放泵外避免
	// 阻塞同连接的其他帧（下载/上传块路由在泵内完成，见 conn.go）。
	var body []byte
	if ar.Body != nil {
		body, _ = json.Marshal(ar.Body)
	}
	req, err := s.buildAdminRequest(ar.Method, ar.Path, bytes.NewReader(body), "application/json", ar.Token)
	if err != nil {
		c.SendJSON(dcResp{Type: "err", Msg: err.Error(), ReqID: ar.ReqID})
		return
	}
	go s.dispatchAdmin(c, req, ar.ReqID)
}

// adminUploadChunk worker 侧处理一个 admin 上传块（inbound.go uploadWorker
// 调用，IO 移出消息泵——与 H5 对 fileIndex 上传的处理一致）。
// 非 last 块：写临时文件。last 块：先写，然后收齐 → 内部 multipart 转发回
// 响应帧；中止（aborted）→ 清理 + err。
func (s *PeerJSService) adminUploadChunk(c Session, au *adminUploadState, data []byte, last bool) {
	if len(data) > 0 {
		if _, err := au.f.Write(data); err != nil {
			// 写失败：必须清理临时文件与句柄，否则泄漏（文件 + fd 常驻）。
			// 与 aborted 分支的清理语义一致；aborted 分支在下方。
			// 发现背景：代码审阅 2026-08-18（此前仅 aborted 分支清理）。
			os.Remove(au.path)
			au.f.Close()
			_ = c.SendJSON(dcResp{Type: "err", Msg: "admin upload write failed: " + err.Error(), ReqID: au.reqID})
			return
		}
	}
	if !last {
		return
	}
	if au.aborted {
		os.Remove(au.path)
		au.f.Close()
		_ = c.SendJSON(dcResp{Type: "err", Msg: "upload aborted: size mismatch or timeout", ReqID: au.reqID})
		return
	}
	s.serveAdminUploadComplete(c, au)
}

// serveAdminUploadComplete admin 上传收齐后的内部转发。
// multipart 构造 + handler 调用都在 worker goroutine 里，不阻塞消息泵。
func (s *PeerJSService) serveAdminUploadComplete(c Session, au *adminUploadState) {
	defer os.Remove(au.path) // 临时文件必清（正常/异常路径都走到这里）
	defer au.f.Close()
	if au.got < au.size {
		c.SendJSON(dcResp{Type: "err", Msg: "upload aborted: incomplete", ReqID: au.reqID})
		return
	}
	// 上传转发路径：声明帧的 path（如 /files/upload 或 /bt/torrent），
	// 空则兜底 /files/upload（老客户端未带 path 时的存储上传语义）。
	// 坑：au.f 的写 offset 已在文件末尾，必须 Seek(0) 从头读，
	// 否则 io.Copy 读到 0 字节（发现背景：admin 上传测试 got 0 bytes）。
	field := au.field
	if field == "" {
		field = "file"
	}
	reqPath := au.reqPath
	if reqPath == "" {
		reqPath = "/files/upload"
	}
	if _, err := au.f.Seek(0, io.SeekStart); err != nil {
		c.SendJSON(dcResp{Type: "err", Msg: "upload rewind failed: " + err.Error(), ReqID: au.reqID})
		return
	}
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		defer pw.Close()
		defer mw.Close()
		fw, err := mw.CreateFormFile(field, filepath.Base(au.filename))
		if err != nil {
			return
		}
		if _, err := io.Copy(fw, au.f); err != nil {
			return
		}
	}()
	req, err := s.buildAdminRequest("POST", reqPath, pr, mw.FormDataContentType(), "")
	if err != nil {
		pw.Close()
		c.SendJSON(dcResp{Type: "err", Msg: err.Error(), ReqID: au.reqID})
		return
	}
	s.dispatchAdmin(c, req, au.reqID)
}

// buildAdminRequest 构造内部请求：方法/路径/body + token → Authorization header。
// token 为空时不设 header（匿名请求，AuthOptional 放行）。
func (s *PeerJSService) buildAdminRequest(method, path string, body io.Reader, contentType, token string) (*http.Request, error) {
	req, err := http.NewRequest(method, path, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req, nil
}

// dispatchAdmin 内部转发：handler（gin engine）→ 响应帧回浏览器。
// 统一以 admin-resp 承载 HTTP 语义（含错误状态码）：前端与 fetch 版行为一致
// （err.status + err.data 区分 409 冲突清单等结构化错误体，见 api.js request()）。
// 二进制响应（application/octet-stream 等非 JSON）拆为 admin-bin 头 + 二进制帧。
func (s *PeerJSService) dispatchAdmin(c Session, req *http.Request, reqID string) {
	status, respBody, contentType, err := s.adminHandler(req)
	if err != nil {
		c.SendJSON(adminResp{Type: "admin-resp", Status: http.StatusInternalServerError, Body: ginH{"error": err.Error()}, ReqID: reqID})
		return
	}
	// 响应分类：JSON → admin-resp；文本 → admin-resp（字符串 body）；
	// 只有真正二进制（文件流）走 admin-bin。
	// 坑：初版按 respBody 是否以 "{" 开头判断，text/plain 响应（如 /ping 的
	// "pong"）被误判为二进制 → 前端收到 Uint8Array 而非字符串，且 admin-bin
	// 会占用 binaryExpect 槽（发现背景：E2E 冒烟 /ping 返回 Buffer）。
	if len(respBody) > 0 && json.Valid(respBody) {
		var b any
		if err := json.Unmarshal(respBody, &b); err != nil {
			b = string(respBody)
		}
		c.SendJSON(adminResp{Type: "admin-resp", Status: status, Body: b, ReqID: reqID})
		return
	}
	if status >= 400 || contentType == "" || strings.HasPrefix(contentType, "text/") {
		// 非 JSON 错误体 / 文本响应：以字符串 body 透传
		c.SendJSON(adminResp{Type: "admin-resp", Status: status, Body: string(respBody), ReqID: reqID})
		return
	}
	// 二进制响应（文件流，如 /collections/.../{path}）：头 + 单二进制帧。
	// 上限 adminBinMax 已在调用方约束（大文件走 req verb）。
	if int64(len(respBody)) > adminBinMax {
		c.SendJSON(adminResp{Type: "admin-resp", Status: http.StatusRequestEntityTooLarge, Body: ginH{"error": "admin binary response exceeds 64MB limit; use req verb streaming"}, ReqID: reqID})
		return
	}
	if err := c.SendFrame(adminBinResp{Type: "admin-bin", Status: status, Size: int64(len(respBody)), ReqID: reqID}, respBody); err != nil {
		log.LogWarn("peerjs-admin: send binary resp failed: %v", err)
	}
}

// SetAdminHandler 注入内部转发 handler（router.SetupRouter 调用，见
// peerjs_routes.go registerPeerJSRoutes）。h 包装 gin engine：构造内部
// *http.Request → engine.ServeHTTP(recorder) → 返回状态/响应体/Content-Type。
// 加锁：adminHandler 在 SetupRouter 装配期设置，之后只读（serveAdmin 并发调用）。
func (s *PeerJSService) SetAdminHandler(h AdminHandler) {
	s.adminMu.Lock()
	defer s.adminMu.Unlock()
	s.adminHandler = h
}
