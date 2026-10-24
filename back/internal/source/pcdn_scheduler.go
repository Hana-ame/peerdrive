package source

import (
	"context"
	"fmt"
	"io"
	"sync"
)

// chunkTask 单个切片调度任务。
type chunkTask struct {
	index  int
	offset int64
	size   int64
}

// chunkResult 切片获取结果。
type chunkResult struct {
	index int
	data  []byte
	err   error
}

// planTasks 将指定区间切分为逻辑切片。
func (s *PCDNSource) planTasks(offset, size int64) []chunkTask {
	var tasks []chunkTask
	rem := size
	cur := offset
	idx := 0
	for rem > 0 {
		take := s.cfg.ChunkSize
		if rem < take {
			take = rem
		}
		tasks = append(tasks, chunkTask{
			index:  idx,
			offset: cur,
			size:   take,
		})
		cur += take
		rem -= take
		idx++
	}
	return tasks
}

// runPipeline 执行分片流水线与有序下发。
func (s *PCDNSource) runPipeline(
	ctx context.Context,
	hash string,
	offset, size int64,
	tasks []chunkTask,
	pw *io.PipeWriter,
) {
	// 若区间无法静态预先切片（未指定 size 且 origin.Info 为未知），降级回逐块流式拉取
	if len(tasks) == 0 && size < 0 {
		s.streamSequentialUnknownSize(ctx, hash, offset, pw)
		return
	}

	// 若切片为空，直接关闭管道
	if len(tasks) == 0 {
		_ = pw.Close()
		return
	}

	// 任务下发通道与结果收集
	taskChan := make(chan chunkTask, len(tasks))
	for _, t := range tasks {
		taskChan <- t
	}
	close(taskChan)

	resultChan := make(chan chunkResult, len(tasks))

	// 启动 Worker 协程池
	numWorkers := s.cfg.WorkerCount
	if numWorkers > len(tasks) {
		numWorkers = len(tasks)
	}
	if numWorkers <= 0 {
		numWorkers = 1
	}

	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := range taskChan {
				select {
				case <-ctx.Done():
					resultChan <- chunkResult{index: task.index, err: ctx.Err()}
					return
				default:
				}
				data, err := s.fetchChunkWithFallback(ctx, hash, task.offset, task.size)
				resultChan <- chunkResult{index: task.index, data: data, err: err}
			}
		}()
	}

	// 独立协程等待所有任务完成后关闭结果通道
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// 有序重组缓冲池：按序号顺序将切片写入管道
	received := make(map[int][]byte)
	nextIndex := 0

	for res := range resultChan {
		if res.err != nil {
			_ = pw.CloseWithError(fmt.Errorf("chunk %d failed: %w", res.index, res.err))
			return
		}
		received[res.index] = res.data

		// 按序刷出就绪切片
		for {
			data, ready := received[nextIndex]
			if !ready {
				break
			}
			delete(received, nextIndex)
			if len(data) > 0 {
				if _, err := pw.Write(data); err != nil {
					_ = pw.CloseWithError(err)
					return
				}
			}
			nextIndex++
		}
	}

	// 所有切片下发完毕
	_ = pw.Close()
}

// streamSequentialUnknownSize 处理总大小完全未知时的顺序切片拉取与下发。
func (s *PCDNSource) streamSequentialUnknownSize(
	ctx context.Context,
	hash string,
	offset int64,
	pw *io.PipeWriter,
) {
	cur := offset
	for {
		select {
		case <-ctx.Done():
			_ = pw.CloseWithError(ctx.Err())
			return
		default:
		}

		data, err := s.fetchChunkWithFallback(ctx, hash, cur, s.cfg.ChunkSize)
		if err != nil {
			// 如果读取到 EOF 或失败，安全终止
			_ = pw.CloseWithError(err)
			return
		}
		if len(data) == 0 {
			_ = pw.Close()
			return
		}

		if _, err := pw.Write(data); err != nil {
			_ = pw.CloseWithError(err)
			return
		}

		// 若获取的数据小于单切片大小，代表已到达文件末尾
		if int64(len(data)) < s.cfg.ChunkSize {
			_ = pw.Close()
			return
		}
		cur += int64(len(data))
	}
}
