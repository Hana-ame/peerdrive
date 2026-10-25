package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// pullJobMeta 落地到 .part.meta 文件的断点续传元数据。
type pullJobMeta struct {
	ID        string    `json:"id"`
	Peer      string    `json:"peer"`
	Hash      string    `json:"hash"`
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	Coll      string    `json:"collection,omitempty"`
	Total     int64     `json:"total"`
	CreatedAt time.Time `json:"created_at"`
}

// saveJobMetaLocked 将任务元数据原子落盘到 metaPath（.part.meta）。调用方必须持有 p.mu。
func (p *PeerPuller) saveJobMetaLocked(job *PullJob, metaPath string) error {
	meta := pullJobMeta{
		ID:        job.ID,
		Peer:      job.Peer,
		Hash:      job.Hash,
		Name:      job.Name,
		Path:      job.Path,
		Coll:      job.Coll,
		Total:     job.Total,
		CreatedAt: job.Started,
	}
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(metaPath), 0o755); err != nil {
		return err
	}
	tmpMeta := metaPath + ".tmp"
	if err := os.WriteFile(tmpMeta, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpMeta, metaPath)
}

// saveJobMeta 加锁将任务元数据落盘到 metaPath。
func (p *PeerPuller) saveJobMeta(job *PullJob, metaPath string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.saveJobMetaLocked(job, metaPath)
}

// RecoverIncompleteTasks 扫描 downloadRoot/pulled 目录下的 .part 文件，
// 将其映射回 PullJob 并恢复为 resuming 状态启动断点续传。
func (p *PeerPuller) RecoverIncompleteTasks() []*PullJob {
	pulledDir := filepath.Join(p.downloadRoot, "pulled")
	if _, err := os.Stat(pulledDir); err != nil {
		return nil
	}
	var recovered []*PullJob
	_ = filepath.Walk(pulledDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".part") {
			return nil
		}
		metaPath := path + ".meta"
		// #nosec G122 -- WalkDir 回调内立即使用的 path/metaPath 本就是回调参数
		// （WalkDir 文档推荐用法）；扫描的是本地 downloadRoot/pulled，同机威胁
		// 模型下无并发换文件面。
		raw, readErr := os.ReadFile(metaPath)
		if readErr != nil {
			return nil
		}
		var meta pullJobMeta
		if jsonErr := json.Unmarshal(raw, &meta); jsonErr != nil || meta.Hash == "" || meta.Peer == "" {
			return nil
		}
		// 若该 hash 已经在本地索引中（之前已下载完成），清理残余 .part
		if p.isLocal != nil && p.isLocal(meta.Hash) {
			// #nosec G122 -- 同回调内移除本地残余文件，非 WalkDir 跨回调滥用
			// （扫描本地 downloadRoot/pulled，同机威胁模型无并发换文件面）。
			_ = os.Remove(path)     // #nosec G122 -- 同上，行内标注
			_ = os.Remove(metaPath) // #nosec G122 -- 同上，行内标注
			return nil
		}
		p.mu.Lock()
		for _, existing := range p.jobs {
			if !existing.Done() && existing.Hash == meta.Hash {
				p.mu.Unlock()
				return nil
			}
		}
		job := &PullJob{
			ID:       meta.ID,
			Peer:     meta.Peer,
			Hash:     meta.Hash,
			Name:     meta.Name,
			Path:     meta.Path,
			Coll:     meta.Coll,
			Total:    meta.Total,
			Received: info.Size(),
			Status:   PullResuming,
			Started:  meta.CreatedAt,
		}
		if job.ID == "" {
			job.ID = newJobID()
		}
		if job.Started.IsZero() {
			job.Started = time.Now()
		}
		ctx, cancel := context.WithCancel(context.Background())
		p.jobs[job.ID] = job
		p.cancels[job.ID] = cancel
		p.pruneLocked()
		snap := *job
		p.mu.Unlock()

		go p.run(ctx, job)
		recovered = append(recovered, &snap)
		return nil
	})
	return recovered
}
