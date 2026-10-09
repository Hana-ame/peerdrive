package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"peerdrive/internal/log"
)

// peerpull_worker.go — Pull execution loop, retry strategy, verification and progress streaming.

// pullAttempts 跨节点拉取的最大尝试次数（首次 + 重试）。
//
// 为什么需要重试：transport 的连接是会断的（ICE 切换、对端重启、
// NAT 超时），conn.go 在连接断开时给所有在途 fetch 推 "connection closed"。
// 改之前单次失败即任务 failed——**任何一次网络抖动都会让用户的保存操作
// 彻底失败**，只能手动重来。CI 上这个缺陷表现为
// TestPeerPullSavesToLocalDrive 偶发 err="peerjs: connection closed"
// （2026-10-06 连续两次红，本地跑 3/3 绿：CI 机器负载高、连接更容易被打断）。
//
// 为什么是 3 而不是无限重试：连接断在「对端确实走了」时会一直断，
// 无限重试会让 job 永远挂着，占着并发名额（sem），用户既看不到进展也取消不掉。
// 3 次 + 递增退避足以覆盖瞬时抖动，又能让真断连在十几秒内给出明确失败。
const pullAttempts = 3

// pullBackoff 第 n 次重试前的等待（n 从 0 起）。
// 递增而不是固定：对端可能正在重启，等久一点比连环重试更容易赶上。
func pullBackoff(n int) time.Duration {
	d := time.Duration(1<<uint(n)) * time.Second // 1s → 2s → 4s
	if d > 8*time.Second {
		d = 8 * time.Second
	}
	return d
}

// retryablePullErr 判断拉取错误是否值得重试。
//
// 重试：连接类瞬时故障（连接断开、流被取消/重置、请求超时）。
// 不重试：本地 I/O 与协议类错误——重跑一遍还是同样的结果，
//   只会把用户的等待时间从 1 秒拖到 7 秒，然后给出一模一样的错误信息。
func retryablePullErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// 对端没在我们请求之前就准备好（会话刚建立时的竞态），重试有意义
	if strings.Contains(msg, "no connection to") || strings.Contains(msg, "connection closed") ||
		strings.Contains(msg, "connection not bound") ||
		strings.Contains(msg, "use of closed network connection") ||
		strings.Contains(msg, "connection reset") || strings.Contains(msg, "EOF") ||
		strings.Contains(msg, "broken pipe") || strings.Contains(msg, "i/o timeout") {
		return true
	}
	return false
}

// run 执行一次拉取：跳过检查 → 流式下载（带进度与断点续传）→ 校验 → 落盘登记。
func (p *PeerPuller) run(ctx context.Context, job *PullJob) {
	// 并发闸：等一个名额（可被取消打断）
	select {
	case p.sem <- struct{}{}:
	case <-ctx.Done():
		target, _ := p.targetPath(job)
		if target != "" {
			_ = os.Remove(target + ".part")
			_ = os.Remove(target + ".part.meta")
		}
		p.finish(job, PullCancelled, "", time.Time{})
		return
	}
	defer func() { <-p.sem }()

	// ① 本地已有同 hash 内容 → 跳过（内容寻址去重：同内容不必再下一遍）
	if p.isLocal != nil && p.isLocal(job.Hash) {
		p.mu.Lock()
		job.Skipped = true
		p.mu.Unlock()
		log.LogInfo("peerpull: skip %s (already local)", job.Hash[:12])
		p.finish(job, PullDone, "", time.Time{})
		return
	}

	// ③ 落盘到 .part（与最终文件同目录 → rename 原子，不会留半截"正式文件"）
	target, err := p.targetPath(job)
	if err != nil {
		p.finish(job, PullFailed, err.Error(), time.Time{})
		return
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		p.finish(job, PullFailed, "mkdir: "+err.Error(), time.Time{})
		return
	}
	tmp := target + ".part"

	// ②+③ 合并成可重试的一轮：开流 → 写 .part → 校验 → rename → 登记。
	// 支持断点续传：若已有 .part 文件，先读取已有字节并预先计算哈希，随后从断点续传。
	var lastErr error
	for attempt := 0; attempt < pullAttempts; attempt++ {
		if attempt > 0 {
			// 用户主动取消就别再重试了：等完退避才失败会让人以为卡死
			if ctx.Err() != nil {
				p.finish(job, PullCancelled, "", time.Time{})
				return
			}
			log.LogWarn("peerpull: %s retry %d/%d after %v",
				job.Hash[:12], attempt+1, pullAttempts, lastErr)
			select {
			case <-time.After(pullBackoff(attempt - 1)):
			case <-ctx.Done():
				p.finish(job, PullCancelled, "", time.Time{})
				return
			}
		}
		ok, err := p.fetchOnce(ctx, job, target, tmp)
		if ok {
			return // fetchOnce 内部已 finish（done / failed）
		}
		lastErr = err
		if !retryablePullErr(err) {
			p.finish(job, PullFailed, err.Error(), time.Time{})
			return
		}
	}
	p.finish(job, PullFailed,
		fmt.Sprintf("%v (after %d attempts)", lastErr, pullAttempts), time.Time{})
}

// fetchOnce 跑一轮完整的「开流 → 落盘 → 校验 → 登记」。
// 返回 ok=true 表示已经 finish 过（无论成功还是终态失败），调用方直接返回；
// ok=false + err 表示这是一次**值得重试**的瞬时故障，err 里是原因。
func (p *PeerPuller) fetchOnce(ctx context.Context, job *PullJob, target, tmp string) (bool, error) {
	metaPath := tmp + ".meta"
	_ = p.saveJobMeta(job, metaPath)

	var partOffset int64 = 0
	h := sha256.New()

	if fi, statErr := os.Stat(tmp); statErr == nil && fi.Mode().IsRegular() && fi.Size() > 0 {
		if job.Total > 0 && fi.Size() > job.Total {
			_ = os.Truncate(tmp, 0)
		} else {
			existingFile, oErr := os.Open(tmp)
			if oErr == nil {
				n, cpErr := io.Copy(h, existingFile)
				_ = existingFile.Close()
				if cpErr == nil && n == fi.Size() {
					partOffset = n
					p.mu.Lock()
					job.Received = partOffset
					p.mu.Unlock()
				} else {
					h.Reset()
					_ = os.Truncate(tmp, 0)
					partOffset = 0
				}
			}
		}
	}

	// 极端情况：.part 文件已经完整落盘（且达到已知 total），直接校验并完成
	if partOffset > 0 && job.Total > 0 && partOffset == job.Total {
		sum := hex.EncodeToString(h.Sum(nil))
		if sum == job.Hash {
			_ = os.Remove(metaPath)
			if err := os.Rename(tmp, target); err != nil {
				_ = os.Remove(tmp)
				p.finish(job, PullFailed, "rename: "+err.Error(), time.Time{})
				return true, nil
			}
			p.finishSaved(job, target, partOffset)
			return true, nil
		}
		h.Reset()
		_ = os.Truncate(tmp, 0)
		partOffset = 0
		p.mu.Lock()
		job.Received = 0
		p.mu.Unlock()
	}

	stream, err := p.source.OpenStream(job.Peer, job.Hash, partOffset, -1)
	if err != nil {
		return false, fmt.Errorf("open stream: %w", err)
	}
	p.mu.Lock()
	p.closers[job.ID] = stream
	p.mu.Unlock()
	defer func() {
		_ = stream.Close()
		p.mu.Lock()
		delete(p.closers, job.ID)
		p.mu.Unlock()
	}()

	var f *os.File
	if partOffset > 0 {
		f, err = os.OpenFile(tmp, os.O_WRONLY|os.O_APPEND, 0o644)
	} else {
		f, err = os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	}
	if err != nil {
		p.finish(job, PullFailed, "open temp: "+err.Error(), time.Time{})
		return true, nil // 本地 I/O 失败，不重试
	}

	written, cpErr := p.copyWithProgress(ctx, f, stream, h, job, partOffset, metaPath)
	closeErr := f.Close()
	if cpErr != nil {
		if ctx.Err() != nil {
			_ = os.Remove(tmp)
			_ = os.Remove(metaPath)
			p.finish(job, PullCancelled, "", time.Time{})
			return true, nil
		}
		// 网络/传输错误：保留 .part 与 .meta 文件供断点续传，不删除！
		_ = p.saveJobMeta(job, metaPath)
		return false, cpErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		_ = os.Remove(metaPath)
		p.finish(job, PullFailed, "close temp: "+closeErr.Error(), time.Time{})
		return true, nil
	}

	// ④ 内容寻址校验：对端给的内容必须真的等于请求的 hash
	sum := hex.EncodeToString(h.Sum(nil))
	if sum != job.Hash {
		_ = os.Remove(tmp)
		_ = os.Remove(metaPath)
		p.finish(job, PullFailed, fmt.Sprintf("hash mismatch: got %s", sum[:12]), time.Time{})
		return true, nil
	}
	_ = os.Remove(metaPath)
	// ⑤ rename 成正式名 + 登记进 file_index
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		p.finish(job, PullFailed, "rename: "+err.Error(), time.Time{})
		return true, nil
	}
	p.finishSaved(job, target, partOffset+written)
	return true, nil
}

// finishSaved 完成保存落盘后的登记、解包与终态处理。
func (p *PeerPuller) finishSaved(job *PullJob, target string, totalBytes int64) {
	ended := time.Now()
	if p.register != nil {
		if _, _, err := p.register(target); err != nil {
			log.LogWarn("peerpull: register %s failed: %v", target, err)
			p.finish(job, PullDone, "saved but not indexed: "+err.Error(), ended)
			p.mu.Lock()
			job.SavedTo = target
			p.mu.Unlock()
			return
		}
	}

	if p.extractor != nil && p.extractor.ShouldExtract(filepath.Base(target)) {
		destDir := target + "_extracted"
		extracted, err := p.extractor.Extract(target, destDir)
		if err != nil {
			log.LogWarn("peerpull: auto-extract %s failed: %v", target, err)
		} else if len(extracted) > 0 && p.register != nil {
			registered := 0
			for _, ef := range extracted {
				if _, _, regErr := p.register(ef.FullName); regErr != nil {
					log.LogWarn("peerpull: register extracted %s failed: %v", ef.FullName, regErr)
				} else {
					registered++
				}
			}
			log.LogInfo("peerpull: auto-extracted %d files (registered %d) from %s",
				len(extracted), registered, target)
		}
	}

	p.mu.Lock()
	job.SavedTo = target
	job.Received = totalBytes
	p.mu.Unlock()
	p.finish(job, PullDone, "", ended)
}

// copyWithProgress 流式拷贝并更新进度（进度按块更新，够前端画进度条）。
func (p *PeerPuller) copyWithProgress(ctx context.Context, dst io.Writer, src io.Reader, h io.Writer, job *PullJob, initialOffset int64, metaPath string) (int64, error) {
	buf := make([]byte, 64*1024)
	var written int64
	for {
		if ctx.Err() != nil {
			return written, errors.New("cancelled")
		}
		n, rerr := src.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return written, werr
			}
			_, _ = h.Write(buf[:n])
			written += int64(n)
			p.mu.Lock()
			if !job.Done() {
				job.Received = initialOffset + written
				job.Status = PullRunning
				// 对端 meta 帧可能晚于首块到达：每轮尝试刷新总大小
				if job.Total <= 0 {
					if tr, ok := src.(totalReporter); ok {
						job.Total = tr.Total()
						if job.Total > 0 {
							_ = p.saveJobMetaLocked(job, metaPath)
						}
					}
				}
			}
			p.mu.Unlock()
		}
		if rerr != nil {
			if rerr == io.EOF {
				return written, nil
			}
			return written, rerr
		}
	}
}
