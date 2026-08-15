package transport

import (
	"time"

	"peerdrive/internal/log"
)

// 帧协议文件索引 verb 的服务端处理（create/upload/list/info/delete/sync）。
// 全部经 Session 传输（WS/WebRTC 同一套），reqId 回显保持请求-响应配对。
// 请求字段由 dcResp 通用结构承载（Hash/Offset/Size/ReqID/Path/Name/Seq）。

// redactDisallowedPath 对外的文件信息做路径脱敏：create 已强制根目录内，
// 但历史库/旧版本可能残留根目录外的 Path；serveFile 已回退 CAS，list/info/sync
// 也不能再把这类绝对路径泄露给对端。
func (s *PeerJSService) redactDisallowedPath(fi FileInfo) FileInfo {
	if fi.Path != "" && !s.fileIndex.IsPathAllowed(fi.Path) {
		fi.Path = ""
	}
	return fi
}

// serveCreate 处理 create：登记外部文件（sha256 → 绝对路径，不复制文件）。
// 请求 {type:"create", path} → 响应 {type:"created", hash,size,name,path,seq} | err
func (s *PeerJSService) serveCreate(c Session, r dcResp) {
	if r.Path == "" {
		_ = c.SendJSON(dcResp{Type: "err", Msg: "path required", ReqID: r.ReqID})
		return
	}
	fi, err := s.fileIndex.Create(r.Path)
	if err != nil {
		_ = c.SendJSON(dcResp{Type: "err", Msg: err.Error(), ReqID: r.ReqID})
		return
	}
	_ = c.SendJSON(dcResp{Type: "created", Hash: fi.Hash, Total: fi.Size, Path: fi.Path, Name: fi.Name, Seq: fi.Seq, ReqID: r.ReqID})
}

// serveUploadBegin 处理 upload：开始流式接收（后续二进制帧写入 UploadSink）。
// 请求 {type:"upload", name, size, reqId} → 响应 meta{total}；完成后 uploaded{hash,path}。
// 注意：同一连接同时只有一个 upload 接收流（连接级 pendingUpload）。
func (s *PeerJSService) serveUploadBegin(c Session, st *connState, r dcResp) {
	// 允许 size==0：空文件是合法内容寻址值（sha256(空)），与拉取侧空文件校验对称
	if r.Size < 0 || r.Size > 8*1024*1024*1024 {
		_ = c.SendJSON(dcResp{Type: "err", Msg: "invalid upload size", ReqID: r.ReqID})
		return
	}
	offset := r.Offset
	if offset < 0 || offset%uploadChunkSize != 0 {
		_ = c.SendJSON(dcResp{Type: "err", Msg: "offset must be chunk-aligned", ReqID: r.ReqID})
		return
	}
	sess, err := s.fileIndex.BeginUpload(r.Name, r.Size)
	if err != nil {
		_ = c.SendJSON(dcResp{Type: "err", Msg: err.Error(), ReqID: r.ReqID})
		return
	}
	if r.Size == 0 {
		// 空文件没有 data 帧可发，这里直接完成，避免连接级 pendingUpload 卡到
		// 30s stale 清理（且旧实现 size<=0 直接把空文件上传拒之门外）
		if offset != 0 {
			_ = c.SendJSON(dcResp{Type: "err", Msg: "offset beyond size", ReqID: r.ReqID})
			return
		}
		done, fi, err := sess.Complete()
		if err != nil {
			_ = c.SendJSON(dcResp{Type: "err", Msg: err.Error(), ReqID: r.ReqID})
			return
		}
		if !done {
			_ = c.SendJSON(dcResp{Type: "err", Msg: "empty upload failed to complete", ReqID: r.ReqID})
			return
		}
		_ = c.SendJSON(dcResp{Type: "uploaded", Hash: fi.Hash, Total: fi.Size, Path: fi.Path, ReqID: r.ReqID})
		return
	}
	// 分片长度：单次 data 块 ≤ 一个 chunk（64KB）；尾部块自动截断
	length := r.Size - offset
	if length > uploadChunkSize {
		length = uploadChunkSize
	}
	if length <= 0 {
		_ = c.SendJSON(dcResp{Type: "err", Msg: "offset beyond size", ReqID: r.ReqID})
		return
	}
	st.mu.Lock()
	if st.pendingUpload != nil {
		// M6 修复：对端发 upload 头后不发数据块会永久占用槽位——之后该连接
		// 所有 upload 全部 "already in progress"（连接级 DoS，重连才恢复）。
		// 超时自动清空（会话本身留待 file_index 10 分钟 reap，不影响多 source）
		if time.Since(st.pendingUpload.created) > 30*time.Second {
			log.LogWarn("peerjs: stale pending upload cleared (reqId=%s)", st.pendingUpload.reqID)
			st.pendingUpload = nil
		} else {
			// 上一个分片未完成（连接复用异常）：拒绝重入（多 source 走多连接）
			_ = c.SendJSON(dcResp{Type: "err", Msg: "upload already in progress", ReqID: r.ReqID})
			st.mu.Unlock()
			return
		}
	}
	st.pendingUpload = &uploadState{reqID: r.ReqID, offset: offset, size: length, sess: sess, created: time.Now()}
	st.mu.Unlock()
	_ = c.SendJSON(dcResp{Type: "meta", Total: r.Size, Offset: sess.ContiguousOffset(), ReqID: r.ReqID})
}

// serveList 处理 list：列出全部未删除映射。
// 请求 {type:"list", offset?, size?} → 响应 {type:"list-resp", files, total}
func (s *PeerJSService) serveList(c Session, r dcResp) {
	files, err := s.fileIndex.List(int(r.Offset), int(r.Size))
	if err != nil {
		_ = c.SendJSON(dcResp{Type: "err", Msg: err.Error(), ReqID: r.ReqID})
		return
	}
	if files == nil {
		files = []FileInfo{}
	}
	out := make([]FileInfo, 0, len(files))
	for _, f := range files {
		out = append(out, s.redactDisallowedPath(f))
	}
	_ = c.SendJSON(dcResp{Type: "list-resp", Files: out, Total: int64(len(out)), ReqID: r.ReqID})
}

// serveInfo 处理 info：按 hash 返回文件信息（download 前先查）。
// 请求 {type:"info", hash} → 响应 {type:"info-resp", hash,size,name,path,seq} | err
func (s *PeerJSService) serveInfo(c Session, r dcResp) {
	fi, err := s.fileIndex.Info(r.Hash)
	if err != nil {
		_ = c.SendJSON(dcResp{Type: "err", Msg: err.Error(), ReqID: r.ReqID})
		return
	}
	safe := s.redactDisallowedPath(*fi)
	_ = c.SendJSON(dcResp{Type: "info-resp", Hash: safe.Hash, Total: safe.Size, Name: safe.Name, Path: safe.Path, Seq: safe.Seq, ReqID: r.ReqID})
}

// serveDelete 处理 delete：逻辑删除映射（同步用 tombstone）。
// 请求 {type:"delete", hash} → 响应 {type:"deleted", hash, seq}
// L6：响应补 seq（REFACTOR §4 协议要求 deleted{hash,seq}），对端 sync 游标跟踪删除。
func (s *PeerJSService) serveDelete(c Session, r dcResp) {
	seq, err := s.fileIndex.Delete(r.Hash)
	if err != nil {
		_ = c.SendJSON(dcResp{Type: "err", Msg: err.Error(), ReqID: r.ReqID})
		return
	}
	_ = c.SendJSON(dcResp{Type: "deleted", Hash: r.Hash, Seq: seq, ReqID: r.ReqID})
}

// serveSync 处理 sync：metadata 增量同步（seq 游标之后的所有变更含 tombstone）。
// 请求 {type:"sync", seq} → 响应 {type:"sync-resp", files, lastSeq}
func (s *PeerJSService) serveSync(c Session, r dcResp) {
	files, last, err := s.fileIndex.SyncSince(r.Seq)
	if err != nil {
		_ = c.SendJSON(dcResp{Type: "err", Msg: err.Error(), ReqID: r.ReqID})
		return
	}
	if files == nil {
		files = []FileInfo{}
	}
	out := make([]FileInfo, 0, len(files))
	for _, f := range files {
		out = append(out, s.redactDisallowedPath(f))
	}
	_ = c.SendJSON(dcResp{Type: "sync-resp", Files: out, LastSeq: last, ReqID: r.ReqID})
}
