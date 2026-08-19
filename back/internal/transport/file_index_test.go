package transport

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"peerdrive/internal/repository"
)

// initTestDB 内存 SQLite（file_index 表随 InitDB 建）。
func initTestDB(t *testing.T) {
	t.Helper()
	require.NoError(t, repository.InitDB(":memory:"))
}

// TestFileIndex_CreateAndInfo 登记外部文件 → 可查信息。
// 发现背景：功能测试——create 只索引绝对路径、不复制文件。
// 注意：create 受 H2 根目录限制，登记文件必须位于服务 uploadDir 内。
func TestFileIndex_CreateAndInfo(t *testing.T) {
	initTestDB(t)
	svc := NewFileIndexService(t.TempDir())

	content := []byte("file-index-create-test")
	src := filepath.Join(t.TempDir(), "src.bin")
	require.NoError(t, os.WriteFile(src, content, 0o644))

	// 根目录外登记必须被拒绝（H2 任意文件读取修复：create 后可 req 读取）
	_, err := svc.Create(src)
	assert.Error(t, err, "根目录外文件不得登记")

	// 根目录内登记正常
	inRoot := filepath.Join(svc.uploadDir, "src.bin")
	require.NoError(t, os.WriteFile(inRoot, content, 0o644))
	fi, err := svc.Create(inRoot)
	require.NoError(t, err)
	assert.Equal(t, int64(len(content)), fi.Size)
	assert.Equal(t, "src.bin", fi.Name)

	// 查询
	got, err := svc.Info(fi.Hash)
	require.NoError(t, err)
	assert.Equal(t, fi.Hash, got.Hash)
	assert.Equal(t, inRoot, got.Path)

	// 非法 hash 拒绝
	_, err = svc.Info("not-a-hash")
	assert.Error(t, err)
}

// TestFileIndex_CreateSymlinkEscape 符号链接逃逸根目录 → 拒绝。
// 发现背景：H2 防御性测试——IsPathAllowed 必须 EvalSymlinks 解析后再判，
// 否则「根内软链 → 根外目标」绕过限制。
func TestFileIndex_CreateSymlinkEscape(t *testing.T) {
	initTestDB(t)
	svc := NewFileIndexService(t.TempDir())

	outside := filepath.Join(t.TempDir(), "secret.txt")
	require.NoError(t, os.WriteFile(outside, []byte("secret"), 0o644))
	link := filepath.Join(svc.uploadDir, "link.txt")
	require.NoError(t, os.Symlink(outside, link))

	_, err := svc.Create(link)
	assert.Error(t, err, "符号链接逃逸根目录必须拒绝")
}

// TestFileIndex_IsPathAllowed 根目录判定：目录内允许、根外拒绝。
func TestFileIndex_IsPathAllowed(t *testing.T) {
	svc := NewFileIndexService(t.TempDir())
	inRoot := filepath.Join(svc.uploadDir, "a.bin")
	require.NoError(t, os.WriteFile(inRoot, []byte("x"), 0o644))
	assert.True(t, svc.IsPathAllowed(inRoot))
	outside := filepath.Join(t.TempDir(), "b.bin")
	require.NoError(t, os.WriteFile(outside, []byte("x"), 0o644))
	assert.False(t, svc.IsPathAllowed(outside))
}

// TestFileIndex_UploadStream 分片上传（chunk 对齐块）→ 完成 → 映射可查 → 内容可读。
// 发现背景：功能测试——WriteAt 分片 + 位图全满判定 + sha256 登记。
func TestFileIndex_UploadStream(t *testing.T) {
	initTestDB(t)
	uploadDir := t.TempDir()
	svc := NewFileIndexService(uploadDir)

	content := make([]byte, 200*1024)
	for i := range content {
		content[i] = byte(i * 5)
	}
	sess, err := svc.BeginUpload("up.bin", int64(len(content)))
	require.NoError(t, err)

	// 分片顺序写入（每片 64KB，模拟帧协议 data 块）
	for off := 0; off < len(content); off += uploadChunkSize {
		end := off + uploadChunkSize
		if end > len(content) {
			end = len(content)
		}
		require.NoError(t, sess.WriteAt(int64(off), content[off:end]))
	}
	done, fi, err := sess.Complete()
	require.NoError(t, err)
	assert.True(t, done, "位图全满应完成")

	got, err := os.ReadFile(fi.Path)
	require.NoError(t, err)
	assert.Equal(t, content, got)
	assert.Equal(t, sha256Hex(content), fi.Hash)

	info, err := svc.Info(fi.Hash)
	require.NoError(t, err)
	assert.Equal(t, fi.Path, info.Path)
}

// TestFileIndex_UploadEmpty 空文件上传：size=0 也是合法内容寻址值。
// 发现背景：拉取侧 hashMatchesSHA256 已支持空文件，但上传侧 BeginUpload(0)
// 可以直接 Complete；serveUploadBegin 旧实现 size<=0 直接拒绝，两端不对称。
func TestFileIndex_UploadEmpty(t *testing.T) {
	initTestDB(t)
	svc := NewFileIndexService(t.TempDir())

	sess, err := svc.BeginUpload("empty.bin", 0)
	require.NoError(t, err)
	done, fi, err := sess.Complete()
	require.NoError(t, err)
	assert.True(t, done, "size=0 位图应天然全满")
	assert.Equal(t, int64(0), fi.Size)
	assert.Equal(t, sha256Hex([]byte{}), fi.Hash)

	got, err := os.ReadFile(fi.Path)
	require.NoError(t, err)
	assert.Empty(t, got)
}

// TestFileIndex_UploadSizeMismatch 声明大小与实际不符 → Commit 失败。
// 发现背景：防御性测试——size 是协议信任边界，必须校验防半包/丢包残留。
func TestFileIndex_UploadSizeMismatch(t *testing.T) {
	initTestDB(t)
	svc := NewFileIndexService(t.TempDir())

	// 超上限拒绝
	_, err := svc.BeginUpload("big.bin", 9*1024*1024*1024)
	assert.Error(t, err)

	// 越界写拒绝（声明 100 字节，写 200）
	sess, err := svc.BeginUpload("m.bin", 100)
	require.NoError(t, err)
	require.NoError(t, sess.WriteAt(0, []byte("ten-bytes!"))) // 对齐 OK
	err = sess.WriteAt(0, make([]byte, 200))
	assert.Error(t, err, "越界写应失败")
}

// TestFileIndex_ListAndDelete 列表 + 逻辑删除（tombstone）→ 列表不再出现。
//
// 发现背景：功能测试——列表 + 逻辑删除（tombstone）语义
func TestFileIndex_ListAndDelete(t *testing.T) {
	initTestDB(t)
	svc := NewFileIndexService(t.TempDir())

	dir := svc.uploadDir // H2：create 只允许根目录内文件
	var hashes []string
	for i := 0; i < 3; i++ {
		p := filepath.Join(dir, "f"+itoa(i)+".bin")
		require.NoError(t, os.WriteFile(p, []byte("data-"+itoa(i)), 0o644))
		fi, err := svc.Create(p)
		require.NoError(t, err)
		hashes = append(hashes, fi.Hash)
	}

	files, err := svc.List(0, 0)
	require.NoError(t, err)
	assert.Len(t, files, 3)

	// L6：delete 返回新 seq（tombstone 同步游标）
	_, err = svc.Delete(hashes[0])
	require.NoError(t, err)
	files, err = svc.List(0, 0)
	require.NoError(t, err)
	assert.Len(t, files, 2)
}

// TestFileIndex_SyncSince 增量同步：seq 游标 → 变更集（含 tombstone）→ 对端应用。
// 发现背景：功能测试——metadata 同步依赖单调 seq 游标；ApplySync 幂等合并。
func TestFileIndex_SyncSince(t *testing.T) {
	initTestDB(t)
	svcA := NewFileIndexService(t.TempDir())
	svcB := NewFileIndexService(t.TempDir()) // 对端

	dir := svcA.uploadDir // H2：create 只允许根目录内文件
	p1 := filepath.Join(dir, "a.bin")
	require.NoError(t, os.WriteFile(p1, []byte("aaa"), 0o644))
	fi1, err := svcA.Create(p1)
	require.NoError(t, err)

	// A → B 同步（seq 从 0）
	files, last, err := svcA.SyncSince(0)
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.True(t, last > 0)
	n, err := svcB.ApplySync(files)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	// B 侧可查（路径原样同步）
	got, err := svcB.Info(fi1.Hash)
	require.NoError(t, err)
	assert.Equal(t, p1, got.Path)

	// 删除 → tombstone 同步（L6：delete 返回 seq，对端 sync 才跟踪得到删除）
	_, err = svcA.Delete(fi1.Hash)
	require.NoError(t, err)
	files, _, err = svcA.SyncSince(last)
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.True(t, files[0].Delete)
	_, err = svcB.ApplySync(files)
	require.NoError(t, err)
	_, err = svcB.Info(fi1.Hash)
	assert.Error(t, err, "删除应同步生效")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestFileIndex_UploadMultiSource 多 source 并发分片上传：乱序 WriteAt 合并，
// 最后一个分片触发完成（位图合并正确）。
// 发现背景：功能需求——多节点并行上传同一文件不同分片。
func TestFileIndex_UploadMultiSource(t *testing.T) {
	initTestDB(t)
	svc := NewFileIndexService(t.TempDir())

	content := make([]byte, 4*uploadChunkSize) // 4 个 source 各一块
	for i := range content {
		content[i] = byte(i * 7)
	}
	sess, err := svc.BeginUpload("multi.bin", int64(len(content)))
	require.NoError(t, err)

	// 4 个 source 乱序并发（模拟 4 个连接）
	const sources = 4
	var wg sync.WaitGroup
	order := []int{2, 0, 3, 1} // 乱序
	for _, si := range order {
		wg.Add(1)
		go func(si int) {
			defer wg.Done()
			off := si * uploadChunkSize
			end := off + uploadChunkSize
			if end > len(content) {
				end = len(content)
			}
			if err := sess.WriteAt(int64(off), content[off:end]); err != nil {
				t.Errorf("source %d write: %v", si, err)
			}
		}(si)
	}
	wg.Wait()

	done, fi, err := sess.Complete()
	require.NoError(t, err)
	assert.True(t, done)
	assert.Equal(t, sha256Hex(content), fi.Hash)

	got, err := os.ReadFile(fi.Path)
	require.NoError(t, err)
	assert.Equal(t, content, got)
}

// TestFileIndex_UploadResume 断点续传：中断后重开会话，连续已写偏移正确，
// 剩余分片补齐后完成。
// 发现背景：功能需求——上传中断后从已接收位置继续。
func TestFileIndex_UploadResume(t *testing.T) {
	initTestDB(t)
	svc := NewFileIndexService(t.TempDir())

	content := make([]byte, 3*uploadChunkSize)
	for i := range content {
		content[i] = byte(i * 3)
	}
	sess, err := svc.BeginUpload("resume.bin", int64(len(content)))
	require.NoError(t, err)
	// 只写前两个 chunk，中断
	require.NoError(t, sess.WriteAt(0, content[:uploadChunkSize]))
	require.NoError(t, sess.WriteAt(int64(uploadChunkSize), content[uploadChunkSize:2*uploadChunkSize]))

	// 重开会话（模拟断线重连/新 source 加入）
	sess2, err := svc.BeginUpload("resume.bin", int64(len(content)))
	require.NoError(t, err)
	cont := sess2.ContiguousOffset()
	assert.Equal(t, int64(2*uploadChunkSize), cont, "续传起点应为 2 个 chunk")

	// 从续传点补齐
	require.NoError(t, sess2.WriteAt(cont, content[cont:]))
	done, fi, err := sess2.Complete()
	require.NoError(t, err)
	assert.True(t, done)
	assert.Equal(t, sha256Hex(content), fi.Hash)
}

// TestFileIndex_UploadPartialNotComplete 位图未满时 Complete 返回未完成。
// 发现背景：防御性测试——缺分片时不得登记映射。
func TestFileIndex_UploadPartialNotComplete(t *testing.T) {
	initTestDB(t)
	svc := NewFileIndexService(t.TempDir())

	content := make([]byte, 2*uploadChunkSize)
	sess, err := svc.BeginUpload("partial.bin", int64(len(content)))
	require.NoError(t, err)
	require.NoError(t, sess.WriteAt(0, content[:uploadChunkSize])) // 只写一半

	done, fi, err := sess.Complete()
	require.NoError(t, err)
	assert.False(t, done, "缺分片不得完成")
	assert.Nil(t, fi)
}

// TestFileIndex_BeginUploadSizeMismatch 同名会话复用声明 size 不一致 → 拒绝。
// 发现背景：M7 防御性测试——位图按旧 size 建，声明不一致会导致续传偏移
// 错乱、末 chunk 判满错误（TRANSPORT-REVIEW M7）。
func TestFileIndex_BeginUploadSizeMismatch(t *testing.T) {
	initTestDB(t)
	svc := NewFileIndexService(t.TempDir())

	_, err := svc.BeginUpload("same.bin", 100)
	require.NoError(t, err)
	_, err = svc.BeginUpload("same.bin", 200)
	assert.Error(t, err, "同名会话 size 不一致必须拒绝")
	// 一致则可复用（续传）
	_, err = svc.BeginUpload("same.bin", 100)
	assert.NoError(t, err)
}

// TestFileIndex_AbortIdempotent Abort 幂等（reap 与显式 Abort 并发不 panic）。
// 发现背景：M7 防御性测试——Abort 与 reap 摘除句柄可能竞争，必须幂等。
func TestFileIndex_AbortIdempotent(t *testing.T) {
	initTestDB(t)
	svc := NewFileIndexService(t.TempDir())

	sess, err := svc.BeginUpload("abort.bin", 100)
	require.NoError(t, err)
	sess.Abort()
	sess.Abort() // 幂等
	// 中止后写入必须报错（而非写已关闭句柄的莫名失败）
	err = sess.WriteAt(0, []byte("x"))
	assert.Error(t, err, "abort 后写入必须明确失败")
}

// TestFileIndex_FullWordsIncrementalBoundaries 位图增量计数（fullWords）
// 的边界回归：size 正好 64 chunk 倍数 / 非倍数 / 重复置位不重复计数 /
// 空文件——Complete O(1) 判满重构的保护（发现背景：代码审阅——Complete
// 每分片全扫位图，8GB 上传 = 13 万分片 × 2048 word，改为 setBit 增量
// 计数后必须保证判满语义不变）。
func TestFileIndex_FullWordsIncrementalBoundaries(t *testing.T) {
	initTestDB(t)
	svc := NewFileIndexService(t.TempDir())

	// 1) 非 64 倍数 size：末 word 不足 64 chunk，Complete 判满正确
	content := make([]byte, 2*uploadChunkSize+12345)
	sess, err := svc.BeginUpload("incr1.bin", int64(len(content)))
	require.NoError(t, err)
	require.NoError(t, sess.WriteAt(0, content[:uploadChunkSize]))
	done, _, err := sess.Complete()
	require.NoError(t, err)
	assert.False(t, done, "未写满不得完成")
	require.NoError(t, sess.WriteAt(int64(uploadChunkSize), content[uploadChunkSize:]))
	done, _, err = sess.Complete()
	require.NoError(t, err)
	assert.True(t, done, "全部写满必须完成")

	// 2) 正好 64 chunk 倍数：全部 word 满阈值都是 64，末 word 判满不依赖
	//    增量计数特判
	content = make([]byte, 64*uploadChunkSize)
	sess, err = svc.BeginUpload("incr2.bin", int64(len(content)))
	require.NoError(t, err)
	for i := 0; i < 64; i++ {
		require.NoError(t, sess.WriteAt(int64(i*uploadChunkSize), content[i*uploadChunkSize:(i+1)*uploadChunkSize]))
	}
	done, _, err = sess.Complete()
	require.NoError(t, err)
	assert.True(t, done, "64 倍数 size 全部写满必须完成")

	// 3) 重复置位（重复分片/续传重建）不破坏计数
	content = make([]byte, 2*uploadChunkSize)
	sess, err = svc.BeginUpload("incr3.bin", int64(len(content)))
	require.NoError(t, err)
	require.NoError(t, sess.WriteAt(0, content[:uploadChunkSize]))
	require.NoError(t, sess.WriteAt(0, content[:uploadChunkSize])) // 重复写同一分片
	require.NoError(t, sess.WriteAt(0, content[:uploadChunkSize])) // 再写一次
	done, _, err = sess.Complete()
	require.NoError(t, err)
	assert.False(t, done, "重复置位不构成完成")
	require.NoError(t, sess.WriteAt(int64(uploadChunkSize), content[uploadChunkSize:]))
	done, _, err = sess.Complete()
	require.NoError(t, err)
	assert.True(t, done, "补全后必须完成")

	// 4) 空文件：位图空，O(1) 判满路径直接通过（diff 于 size>0 路径）
	sess, err = svc.BeginUpload("incr4.bin", 0)
	require.NoError(t, err)
	done, fi, err := sess.Complete()
	require.NoError(t, err)
	assert.True(t, done, "空文件必须直接判满")
	assert.Equal(t, sha256Hex(nil), fi.Hash, "空文件 sha256 是合法内容寻址值")
}

// sha256Hex 计算内容哈希（拆分到 transport 包后自带的测试辅助）。
func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// TestFileIndex_WriteFile 直接写文件：流式写入 uploadDir 并登记。
// 发现背景：Source 控制面“直接写文件”的底层实现，2026-08-19。
func TestFileIndex_WriteFile(t *testing.T) {
	initTestDB(t)
	svc := NewFileIndexService(t.TempDir())
	content := []byte("file-index-write-file")
	fi, err := svc.WriteFile("写入测试.bin", bytes.NewReader(content))
	require.NoError(t, err)
	assert.Equal(t, int64(len(content)), fi.Size)
	assert.Equal(t, "写入测试.bin", fi.Name)

	got, err := os.ReadFile(fi.Path)
	require.NoError(t, err)
	assert.Equal(t, content, got)

	info, err := svc.Info(fi.Hash)
	require.NoError(t, err)
	assert.Equal(t, fi.Path, info.Path)
}
