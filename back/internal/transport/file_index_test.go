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

// initTestDB in-memory SQLite (the file_index table is created with InitDB).
func initTestDB(t *testing.T) {
	t.Helper()
	require.NoError(t, repository.InitDB(":memory:"))
}

// newTestIndex builds a test FileIndexService and **registers Close**.
//
// Why NewFileIndexService(t.TempDir()) alone is not enough: upload sessions hold file handles,
// and if those are not closed, Windows cannot clean TempDir ("being used by another process");
// Linux leaks the same way but silently -- this only shows up when the tests are actually run on
// Windows (2026-09-20).
func newTestIndex(t *testing.T) *FileIndexService {
	t.Helper()
	svc := NewFileIndexService(t.TempDir())
	t.Cleanup(svc.Close)
	return svc
}

// TestFileIndex_CreateAndInfo register an external file -> its info becomes queryable.
// Discovery background: functional test -- create only indexes the absolute path, it does not copy the file.
// Note: create is subject to the H2 root-directory restriction, so a registered file must live inside the service's uploadDir.
func TestFileIndex_CreateAndInfo(t *testing.T) {
	initTestDB(t)
	svc := newTestIndex(t)

	content := []byte("file-index-create-test")
	src := filepath.Join(t.TempDir(), "src.bin")
	require.NoError(t, os.WriteFile(src, content, 0o644))

	// registering outside the root must be rejected (H2 arbitrary-file-read fix: after create it could be read via req)
	_, err := svc.Create(src)
	assert.Error(t, err, "files outside root must not be registered")

	// registering inside the root works normally
	inRoot := filepath.Join(svc.uploadDir, "src.bin")
	require.NoError(t, os.WriteFile(inRoot, content, 0o644))
	fi, err := svc.Create(inRoot)
	require.NoError(t, err)
	assert.Equal(t, int64(len(content)), fi.Size)
	assert.Equal(t, "src.bin", fi.Name)

	// query
	got, err := svc.Info(fi.Hash)
	require.NoError(t, err)
	assert.Equal(t, fi.Hash, got.Hash)
	assert.Equal(t, inRoot, got.Path)

	// an illegal hash is rejected
	_, err = svc.Info("not-a-hash")
	assert.Error(t, err)
}

// TestFileIndex_CreateSymlinkEscape a symlink escaping the root directory -> rejected.
// Discovery background: H2 defensive test -- IsPathAllowed must EvalSymlinks and only then judge,
// or a "symlink inside the root -> target outside the root" slips past the restriction.
func TestFileIndex_CreateSymlinkEscape(t *testing.T) {
	initTestDB(t)
	svc := newTestIndex(t)

	outside := filepath.Join(t.TempDir(), "secret.txt")
	require.NoError(t, os.WriteFile(outside, []byte("secret"), 0o644))
	link := filepath.Join(svc.uploadDir, "link.txt")
	require.NoError(t, os.Symlink(outside, link))

	_, err := svc.Create(link)
	assert.Error(t, err, "symlink escaping root must be rejected")
}

// TestFileIndex_IsPathAllowed root-directory judgement: inside the directory is allowed, outside the root is rejected.
func TestFileIndex_IsPathAllowed(t *testing.T) {
	svc := newTestIndex(t)
	inRoot := filepath.Join(svc.uploadDir, "a.bin")
	require.NoError(t, os.WriteFile(inRoot, []byte("x"), 0o644))
	assert.True(t, svc.IsPathAllowed(inRoot))
	outside := filepath.Join(t.TempDir(), "b.bin")
	require.NoError(t, os.WriteFile(outside, []byte("x"), 0o644))
	assert.False(t, svc.IsPathAllowed(outside))
}

// TestFileIndex_ReadRoot the registration boundary and the read boundary must be separate.
//
// Discovery background (real incident): the operator put the shared directory outside the downloads
// directory (e.g. /mnt/media); registration succeeded and the share frame listed the file, but the
// peer's pull gave read failed -- the read side used the registration side's "only the downloads
// directory" judgement, ruled it out of bounds, and fell back to a content-addressed copy that
// did not exist. Fix: readable = downloads root ∪ operator-declared shared directories; while
// registerable/writable remains only the downloads root.
func TestFileIndex_ReadRoot(t *testing.T) {
	initTestDB(t)
	svc := newTestIndex(t)

	share := t.TempDir() // simulate the directory outside the downloads dir that PEERDRIVE_SHARE_DIRS points at
	shared := filepath.Join(share, "movie.mkv")
	require.NoError(t, os.WriteFile(shared, []byte("movie"), 0o644))

	// 1) before declaration: neither side allows it
	assert.False(t, svc.IsPathReadable(shared), "undeclared directory should not be readable externally")

	// 2) after declaring it a readable root: reads are allowed
	svc.AddReadRoot(share)
	assert.True(t, svc.IsPathReadable(shared), "operator-declared shared directory must be readable")

	// 3) but the registration/write boundary must **not** open up with it: a peer still cannot write files into my shared directory
	_, err := svc.Create(shared)
	assert.Error(t, err, "shared directory readable ≠ registrable, write boundary must not be opened up")

	// 4) a file inside the downloads root is allowed on both sides
	inRoot := filepath.Join(svc.uploadDir, "a.bin")
	require.NoError(t, os.WriteFile(inRoot, []byte("x"), 0o644))
	assert.True(t, svc.IsPathAllowed(inRoot))
	assert.True(t, svc.IsPathReadable(inRoot))

	// 5) an empty config must not become "allow everything"
	svc.AddReadRoot("")
	svc.AddReadRoot("   ")
	assert.False(t, svc.IsPathReadable(""), "empty path must be rejected")
	other := filepath.Join(t.TempDir(), "other.bin")
	require.NoError(t, os.WriteFile(other, []byte("x"), 0o644))
	assert.False(t, svc.IsPathReadable(other), "undeclared directory still not readable")
}

// TestFileIndex_ReadRootSiblingPrefix same-prefix sibling directories must not allow each other
// (/media and /media-private would collide under naive prefix matching).
func TestFileIndex_ReadRootSiblingPrefix(t *testing.T) {
	svc := newTestIndex(t)
	base := t.TempDir()
	share := filepath.Join(base, "media")
	require.NoError(t, os.MkdirAll(share, 0o755))
	sibling := filepath.Join(base, "media-private")
	require.NoError(t, os.MkdirAll(sibling, 0o755))

	svc.AddReadRoot(share)
	assert.True(t, svc.IsPathReadable(filepath.Join(share, "a.mkv")))
	assert.False(t, svc.IsPathReadable(filepath.Join(sibling, "secret.txt")),
		"same-prefix sibling directory must not be allowed")
}

// TestFileIndex_UploadStream chunked upload (chunk-aligned blocks) -> complete -> mapping queryable -> content readable.
// Discovery background: functional test -- WriteAt chunking + the bitmap-full judgement + sha256 registration.
func TestFileIndex_UploadStream(t *testing.T) {
	initTestDB(t)
	uploadDir := t.TempDir()
	svc := NewFileIndexService(uploadDir)
	t.Cleanup(svc.Close) // same as newTestIndex: on Windows TempDir cannot be cleaned without closing the handles

	content := make([]byte, 200*1024)
	for i := range content {
		content[i] = byte(i * 5)
	}
	sess, err := svc.BeginUpload("up.bin", int64(len(content)))
	require.NoError(t, err)

	// write chunks in order (each 64KB, simulating the frame protocol's data blocks)
	for off := 0; off < len(content); off += uploadChunkSize {
		end := off + uploadChunkSize
		if end > len(content) {
			end = len(content)
		}
		require.NoError(t, sess.WriteAt(int64(off), content[off:end]))
	}
	done, fi, err := sess.Complete()
	require.NoError(t, err)
	assert.True(t, done, "bitmap full should complete")

	got, err := os.ReadFile(fi.Path)
	require.NoError(t, err)
	assert.Equal(t, content, got)
	assert.Equal(t, sha256Hex(content), fi.Hash)

	info, err := svc.Info(fi.Hash)
	require.NoError(t, err)
	assert.Equal(t, fi.Path, info.Path)
}

// TestFileIndex_UploadEmpty empty-file upload: size=0 is a legitimate content-addressed value.
// Discovery background: the pull side's hashMatchesSHA256 already supports empty files, but the
// upload side's BeginUpload(0) can Complete directly; the old serveUploadBegin rejected size<=0
// outright, so the two sides were asymmetric.
func TestFileIndex_UploadEmpty(t *testing.T) {
	initTestDB(t)
	svc := newTestIndex(t)

	sess, err := svc.BeginUpload("empty.bin", 0)
	require.NoError(t, err)
	done, fi, err := sess.Complete()
	require.NoError(t, err)
	assert.True(t, done, "size=0 bitmap should be naturally full")
	assert.Equal(t, int64(0), fi.Size)
	assert.Equal(t, sha256Hex([]byte{}), fi.Hash)

	got, err := os.ReadFile(fi.Path)
	require.NoError(t, err)
	assert.Empty(t, got)
}

// TestFileIndex_UploadSizeMismatch declared size does not match the actual one -> Commit fails.
// Discovery background: defensive test -- size is a protocol trust boundary and must be validated
// to prevent half-package / lost-package residue.
func TestFileIndex_UploadSizeMismatch(t *testing.T) {
	initTestDB(t)
	svc := newTestIndex(t)

	// over the cap is rejected
	_, err := svc.BeginUpload("big.bin", 9*1024*1024*1024)
	assert.Error(t, err)

	// an out-of-bounds write is rejected (declared 100 bytes, wrote 200)
	sess, err := svc.BeginUpload("m.bin", 100)
	require.NoError(t, err)
	require.NoError(t, sess.WriteAt(0, []byte("ten-bytes!"))) // aligned, OK
	err = sess.WriteAt(0, make([]byte, 200))
	assert.Error(t, err, "out-of-bounds write should fail")
}

// TestFileIndex_ListAndDelete listing + logical deletion (tombstone) -> the entry no longer appears in the list.
//
// Discovery background: functional test -- list + logical-deletion (tombstone) semantics
func TestFileIndex_ListAndDelete(t *testing.T) {
	initTestDB(t)
	svc := newTestIndex(t)

	dir := svc.uploadDir // H2: create only allows files inside the root directory
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

	// L6: delete returns a new seq (the tombstone sync cursor)
	_, err = svc.Delete(hashes[0])
	require.NoError(t, err)
	files, err = svc.List(0, 0)
	require.NoError(t, err)
	assert.Len(t, files, 2)
}

// TestFileIndex_SyncSince incremental sync: seq cursor -> change set (including tombstones) -> applied by the peer.
// Discovery background: functional test -- metadata sync depends on a monotonic seq cursor; ApplySync merges idempotently.
func TestFileIndex_SyncSince(t *testing.T) {
	initTestDB(t)
	svcA := newTestIndex(t)
	svcB := newTestIndex(t) // the peer

	dir := svcA.uploadDir // H2: create only allows files inside the root directory
	p1 := filepath.Join(dir, "a.bin")
	require.NoError(t, os.WriteFile(p1, []byte("aaa"), 0o644))
	fi1, err := svcA.Create(p1)
	require.NoError(t, err)

	// A -> B sync (seq from 0)
	files, last, err := svcA.SyncSince(0)
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.True(t, last > 0)
	n, err := svcB.ApplySync(files)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	// queryable on the B side (the path is synced verbatim)
	got, err := svcB.Info(fi1.Hash)
	require.NoError(t, err)
	assert.Equal(t, p1, got.Path)

	// delete -> tombstone sync (L6: delete returns a seq, so the peer's sync can track the deletion)
	_, err = svcA.Delete(fi1.Hash)
	require.NoError(t, err)
	files, _, err = svcA.SyncSince(last)
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.True(t, files[0].Delete)
	_, err = svcB.ApplySync(files)
	require.NoError(t, err)
	_, err = svcB.Info(fi1.Hash)
	assert.Error(t, err, "deletion should sync and take effect")
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

// TestFileIndex_UploadMultiSource concurrent chunked upload from multiple sources: out-of-order
// WriteAt calls merge, and the last chunk triggers completion (the bitmap merges correctly).
// Discovery background: functional requirement -- multiple nodes upload different chunks of the
// same file in parallel.
func TestFileIndex_UploadMultiSource(t *testing.T) {
	initTestDB(t)
	svc := newTestIndex(t)

	content := make([]byte, 4*uploadChunkSize) // 4 sources, one block each
	for i := range content {
		content[i] = byte(i * 7)
	}
	sess, err := svc.BeginUpload("multi.bin", int64(len(content)))
	require.NoError(t, err)

	// 4 sources concurrently and out of order (simulating 4 connections)
	const sources = 4
	var wg sync.WaitGroup
	order := []int{2, 0, 3, 1} // out of order
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

	got, err = os.ReadFile(fi.Path)
	require.NoError(t, err)
	assert.Equal(t, content, got)
}

// TestFileIndex_UploadResume resumable upload: re-open the session after an interruption, the
// contiguous written offset is correct, and it completes once the remaining chunks are filled in.
// Discovery background: functional requirement -- continue from the already-received position after an upload interruption.
func TestFileIndex_UploadResume(t *testing.T) {
	initTestDB(t)
	svc := newTestIndex(t)

	content := make([]byte, 3*uploadChunkSize)
	for i := range content {
		content[i] = byte(i * 3)
	}
	sess, err := svc.BeginUpload("resume.bin", int64(len(content)))
	require.NoError(t, err)
	// write only the first two chunks, then interrupt
	require.NoError(t, sess.WriteAt(0, content[:uploadChunkSize]))
	require.NoError(t, sess.WriteAt(int64(uploadChunkSize), content[uploadChunkSize:2*uploadChunkSize]))

	// re-open the session (simulates a reconnect / a new source joining)
	sess2, err := svc.BeginUpload("resume.bin", int64(len(content)))
	require.NoError(t, err)
	cont := sess2.ContiguousOffset()
	assert.Equal(t, int64(2*uploadChunkSize), cont, "resume start should be 2 chunks")

	// fill in from the resume point
	require.NoError(t, sess2.WriteAt(cont, content[cont:]))
	done, fi, err := sess2.Complete()
	require.NoError(t, err)
	assert.True(t, done)
	assert.Equal(t, sha256Hex(content), fi.Hash)
}

// TestFileIndex_UploadPartialNotComplete Complete reports not-complete when the bitmap is not full.
// Discovery background: defensive test -- a missing chunk must not register a mapping.
func TestFileIndex_UploadPartialNotComplete(t *testing.T) {
	initTestDB(t)
	svc := newTestIndex(t)

	content := make([]byte, 2*uploadChunkSize)
	sess, err := svc.BeginUpload("partial.bin", int64(len(content)))
	require.NoError(t, err)
	require.NoError(t, sess.WriteAt(0, content[:uploadChunkSize])) // only half is written

	done, fi, err := sess.Complete()
	require.NoError(t, err)
	assert.False(t, done, "missing chunk must not complete")
	assert.Nil(t, fi)
}

// TestFileIndex_BeginUploadSizeMismatch reusing a same-name session with a different declared size -> rejected.
// Discovery background: M7 defensive test -- the bitmap is built for the old size, so a mismatched
// declaration would corrupt the resume offset and the last chunk's full judgement (TRANSPORT-REVIEW M7).
func TestFileIndex_BeginUploadSizeMismatch(t *testing.T) {
	initTestDB(t)
	svc := newTestIndex(t)

	_, err := svc.BeginUpload("same.bin", 100)
	require.NoError(t, err)
	_, err = svc.BeginUpload("same.bin", 200)
	assert.Error(t, err, "same-name session with inconsistent size must be rejected")
	// matching is reusable (resume)
	_, err = svc.BeginUpload("same.bin", 100)
	assert.NoError(t, err)
}

// TestFileIndex_AbortIdempotent Abort is idempotent (a concurrent reap and explicit Abort do not panic).
// Discovery background: M7 defensive test -- Abort and reap may race on detaching the handle, so it must be idempotent.
func TestFileIndex_AbortIdempotent(t *testing.T) {
	initTestDB(t)
	svc := newTestIndex(t)

	sess, err := svc.BeginUpload("abort.bin", 100)
	require.NoError(t, err)
	sess.Abort()
	sess.Abort() // idempotent
	// a write after abort must error out (not a cryptic failure from writing a closed handle)
	err = sess.WriteAt(0, []byte("x"))
	assert.Error(t, err, "write after abort must fail clearly")
}

// TestFileIndex_FullWordsIncrementalBoundaries boundary regression for the bitmap's incremental
// counter (fullWords): a size that is exactly a multiple of 64 chunks / not a multiple / repeated
// setting does not double count / empty file -- protection for the Complete O(1) full-judgement
// refactor (discovery background: code review -- Complete used to scan the whole bitmap per chunk;
// an 8GB upload = 130k chunks × 2048 words; after switching to incremental counting in setBit,
// the full-judgement semantics must stay unchanged).
func TestFileIndex_FullWordsIncrementalBoundaries(t *testing.T) {
	initTestDB(t)
	svc := newTestIndex(t)

	// 1) a size that is not a multiple of 64: the last word has fewer than 64 chunks, and Complete's full judgement is correct
	content := make([]byte, 2*uploadChunkSize+12345)
	sess, err := svc.BeginUpload("incr1.bin", int64(len(content)))
	require.NoError(t, err)
	require.NoError(t, sess.WriteAt(0, content[:uploadChunkSize]))
	done, _, err := sess.Complete()
	require.NoError(t, err)
	assert.False(t, done, "not fully written must not complete")
	require.NoError(t, sess.WriteAt(int64(uploadChunkSize), content[uploadChunkSize:]))
	done, _, err = sess.Complete()
	require.NoError(t, err)
	assert.True(t, done, "fully written must complete")

	// 2) exactly a multiple of 64 chunks: every word's full threshold is 64, and the last word's
	//    full judgement does not rely on an incremental-counting special case
	content = make([]byte, 64*uploadChunkSize)
	sess, err = svc.BeginUpload("incr2.bin", int64(len(content)))
	require.NoError(t, err)
	for i := 0; i < 64; i++ {
		require.NoError(t, sess.WriteAt(int64(i*uploadChunkSize), content[i*uploadChunkSize:(i+1)*uploadChunkSize]))
	}
	done, _, err = sess.Complete()
	require.NoError(t, err)
	assert.True(t, done, "64-multiple size fully written must complete")

	// 3) repeated setting (duplicate chunks / resume rebuilds) does not break the count
	content = make([]byte, 2*uploadChunkSize)
	sess, err = svc.BeginUpload("incr3.bin", int64(len(content)))
	require.NoError(t, err)
	require.NoError(t, sess.WriteAt(0, content[:uploadChunkSize]))
	require.NoError(t, sess.WriteAt(0, content[:uploadChunkSize])) // write the same chunk again
	require.NoError(t, sess.WriteAt(0, content[:uploadChunkSize])) // and once more
	done, _, err = sess.Complete()
	require.NoError(t, err)
	assert.False(t, done, "repeated setting does not constitute completion")
	require.NoError(t, sess.WriteAt(int64(uploadChunkSize), content[uploadChunkSize:]))
	done, _, err = sess.Complete()
	require.NoError(t, err)
	assert.True(t, done, "must complete after filling in")

	// 4) empty file: the bitmap is empty and the O(1) full-judgement path passes straight through (a branch differing from the size>0 path)
	sess, err = svc.BeginUpload("incr4.bin", 0)
	require.NoError(t, err)
	done, fi, err := sess.Complete()
	require.NoError(t, err)
	assert.True(t, done, "empty file must be judged full directly")
	assert.Equal(t, sha256Hex(nil), fi.Hash, "empty file sha256 is a legal content-addressed value")
}

// sha256Hex computes the content hash (a test helper that came along after splitting into the transport package).
func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// TestFileIndex_WriteFile direct file write: streamed into uploadDir and registered.
// Discovery background: the low-level implementation behind the Source control plane's "write file directly", 2026-08-19.
func TestFileIndex_WriteFile(t *testing.T) {
	initTestDB(t)
	svc := newTestIndex(t)
	content := []byte("file-index-write-file")
	fi, err := svc.WriteFile("write-test.bin", bytes.NewReader(content))
	require.NoError(t, err)
	assert.Equal(t, int64(len(content)), fi.Size)
	assert.Equal(t, "write-test.bin", fi.Name)

	got, err := os.ReadFile(fi.Path)
	require.NoError(t, err)
	assert.Equal(t, content, got)

	info, err := svc.Info(fi.Hash)
	require.NoError(t, err)
	assert.Equal(t, fi.Path, info.Path)
}
