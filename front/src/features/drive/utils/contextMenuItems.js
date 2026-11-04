// contextMenuItems.js — Pure builder for Drive context menu items (Issue #252).
// Extracted from Drive.jsx per AGENTS.md §1 pure function extraction rules.

export function buildDriveContextMenuItems({
  file,
  setPreviewFile,
  onDownload,
  openCastModal,
  onCopyDeepLink,
  onShare,
  openTagEditor,
  onDelete,
  fileRef,
  load,
}) {
  if (file) {
    return [
      { label: '预览文件 (Preview)', icon: '👁️', onClick: () => setPreviewFile(file) },
      { label: '下载 (Download)', icon: '⬇️', onClick: () => onDownload(file) },
      { label: '投屏到大屏 (Cast)', icon: '📺', onClick: () => openCastModal(file) },
      { divider: true },
      { label: '复制文件链接 (Copy Link)', icon: '🔗', onClick: () => onCopyDeepLink(file) },
      { label: '对外共享 (Share)', icon: '📤', onClick: () => onShare(file) },
      { label: '编辑标签 (Tags)', icon: '🏷️', onClick: () => openTagEditor(file) },
      { divider: true },
      { label: '删除 (Delete)', icon: '🗑️', danger: true, onClick: () => onDelete(file) },
    ];
  }
  return [
    { label: '上传文件 (Upload)', icon: '⬆️', onClick: () => fileRef.current?.click() },
    { label: '刷新列表 (Refresh)', icon: '🔄', onClick: load },
  ];
}
