package transport

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"peerdrive/internal/repository"
)

// TestFileIndex_InboxQuarantine verifies that uploads from remote peers land in inbox/ and are marked is_inbox.
// 发现背景：Issue #267（远程访客上传落盘沙箱隔离，Inbox 物理分区与待审标记）。
func TestFileIndex_InboxQuarantine(t *testing.T) {
	initTestDB(t)
	svc := newTestIndex(t)

	content := []byte("remote guest uploaded content")
	uploader := "peer-remote-visitor-1"

	// 1. Begin upload for remote peer
	sess, err := svc.BeginUploadForPeer("guest-file.txt", int64(len(content)), uploader)
	require.NoError(t, err)
	defer sess.Abort()

	// Path must be located inside inbox/ subfolder
	assert.True(t, strings.Contains(filepath.ToSlash(sess.path), "/inbox/"),
		"upload for remote peer must be quarantined inside inbox directory, got: %s", sess.path)

	// Write content and complete
	err = sess.WriteAt(0, content)
	require.NoError(t, err)

	done, fi, err := sess.Complete()
	require.NoError(t, err)
	require.True(t, done)
	require.NotNil(t, fi)

	// 2. Repository check: must have uploader_peer_id and is_inbox = true
	record, err := repository.GetFileIndex(fi.Hash)
	require.NoError(t, err)
	require.NotNil(t, record)
	assert.Equal(t, uploader, record.UploaderPeerID, "uploader_peer_id must be recorded")
	assert.True(t, record.IsInbox, "is_inbox must be set to true")

	// 3. ListInboxFiles must return the file
	inboxFiles, err := repository.ListInboxFiles(0, 100)
	require.NoError(t, err)
	require.Len(t, inboxFiles, 1)
	assert.Equal(t, fi.Hash, inboxFiles[0].Hash)

	// 4. Host approves the file -> is_inbox becomes false
	err = repository.ApproveInboxFile(fi.Hash)
	require.NoError(t, err)

	inboxAfter, err := repository.ListInboxFiles(0, 100)
	require.NoError(t, err)
	assert.Empty(t, inboxAfter, "approved file must no longer appear in inbox")

	approvedRecord, err := repository.GetFileIndex(fi.Hash)
	require.NoError(t, err)
	assert.False(t, approvedRecord.IsInbox)
	assert.Equal(t, uploader, approvedRecord.UploaderPeerID)
}
