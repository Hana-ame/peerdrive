// Module ②: My Cloud Drive —— Top-level page orchestration (AGENTS.md §1 compliant).
// File CRUD lives in hooks/useDriveFiles.js; remote casting in hooks/useDriveCast.js;
// progressive-disclosure metadata lives in FileInspectorDrawer.jsx.
import React, { useState, useEffect, useRef, useMemo } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import * as ws from '../../../platform/transport-ws';
import { getNodeSession, onNodeSession } from '../../../lib/nodeSession';
import { mimeOf } from '../../../platform/shared/mime';
import { fmtBytes } from '../../../platform/shared/format';
import FilePreviewModal from '../../../components/netdisk/FilePreviewModal';
import FileInspectorDrawer from '../../../components/netdisk/FileInspectorDrawer';
import DataState from '../../../components/netdisk/DataState';
import DriveGridView from '../components/DriveGridView';
import DriveListView from '../components/DriveListView';
import CastModal from '../components/CastModal';
import TagEditorModal from '../components/TagEditorModal';
import DriveToolbar from '../components/DriveToolbar';
import DriveCategoryBar from '../components/DriveCategoryBar';
import ContextMenu from '../../../components/netdisk/ContextMenu';
import { getFileIcon, getFileCategory } from '../utils/categories';
import { buildDriveContextMenuItems } from '../utils/contextMenuItems';
import { useDriveFiles } from '../hooks/useDriveFiles';
import { useDriveCast } from '../hooks/useDriveCast';

export default function Drive() {
  const { hash: deepHash } = useParams();
  const navigate = useNavigate();
  const fileRef = useRef(null);
  const [peerSession, setPeerSession] = useState(() => getNodeSession());

  useEffect(() => onNodeSession((s) => setPeerSession(s)), []);

  const [searchQuery, setSearchQuery] = useState('');
  const [selectedCategory, setSelectedCategory] = useState('all');
  const [selectedTag, setSelectedTag] = useState('');
  const [viewMode, setViewMode] = useState('list');
  const [sortBy, setSortBy] = useState('time');
  const [sortOrder, setSortOrder] = useState('desc');

  const [editingTagFile, setEditingTagFile] = useState(null);
  const [tagInputText, setTagInputText] = useState('');
  const [previewFile, setPreviewFile] = useState(null);
  const [inspectedFile, setInspectedFile] = useState(null);
  const [contextMenu, setContextMenu] = useState(null);
  const [isDraggingOver, setIsDraggingOver] = useState(false);

  const {
    files,
    err,
    setErr,
    busy,
    setBusy,
    shareLink,
    copiedHash,
    fileTags,
    globalTagCounts,
    load,
    onPick,
    onDownload,
    onDelete,
    onApproveInbox,
    onRejectInbox,
    onShare,
    onCopyDeepLink,
    saveFileTags,
  } = useDriveFiles({ peerSession, selectedCategory, fileRef });

  const {
    castFile,
    setCastFile,
    screens,
    targetScreenId,
    setTargetScreenId,
    targetChannel,
    setTargetChannel,
    castStatus,
    openCastModal,
    handleCast,
    handleCastControl,
  } = useDriveCast({ peerSession, setBusy });

  const handleContextMenu = (e, file) => {
    e.preventDefault();
    e.stopPropagation();
    setContextMenu({ x: e.clientX, y: e.clientY, file });
  };

  const handleContainerContextMenu = (e) => {
    if (e.target.closest('[data-context-target]')) return;
    e.preventDefault();
    setContextMenu({ x: e.clientX, y: e.clientY, file: null });
  };

  const handleDrop = async (e) => {
    e.preventDefault();
    setIsDraggingOver(false);
    const droppedFiles = Array.from(e.dataTransfer?.files || []);
    if (droppedFiles.length === 0 || peerSession?.client) return;
    setBusy(true);
    try {
      for (const file of droppedFiles) {
        await ws.upload(file, file.name, 'file', '/files/upload');
      }
      await load();
    } catch (ex) {
      setErr(ex?.message || String(ex));
    } finally {
      setBusy(false);
    }
  };

  const openTagEditor = (f) => {
    setEditingTagFile(f);
    setTagInputText((fileTags[f.hash] || []).join(', '));
  };

  const saveTags = async () => {
    if (!editingTagFile) return;
    const tags = tagInputText.split(',').map((t) => t.trim()).filter(Boolean);
    try {
      await saveFileTags(editingTagFile.hash, tags);
      setEditingTagFile(null);
    } catch (ex) {
      setErr(ex?.message || String(ex));
    }
  };

  const allTagsWithCounts = useMemo(() => {
    if (globalTagCounts && globalTagCounts.length > 0) return globalTagCounts;
    const map = {};
    Object.values(fileTags).forEach((tagList) => {
      if (Array.isArray(tagList)) {
        tagList.forEach((t) => {
          if (t) map[t] = (map[t] || 0) + 1;
        });
      }
    });
    return Object.entries(map).map(([tag, count]) => ({ tag, count }));
  }, [globalTagCounts, fileTags]);

  const filteredFiles = useMemo(() => {
    if (!files) return [];
    const q = searchQuery.trim().toLowerCase();
    return files
      .filter((f) => {
        const matchQuery =
          !q ||
          f.filename.toLowerCase().includes(q) ||
          (f.hash && f.hash.toLowerCase().includes(q)) ||
          (fileTags[f.hash] || []).some((t) => t.toLowerCase().includes(q));
        const matchCategory = selectedCategory === 'all' || getFileCategory(f) === selectedCategory;
        const matchTag = !selectedTag || (fileTags[f.hash] || []).includes(selectedTag);
        return matchQuery && matchCategory && matchTag;
      })
      .sort((a, b) => {
        let cmp = 0;
        if (sortBy === 'name') cmp = (a.filename || '').localeCompare(b.filename || '');
        else if (sortBy === 'size') cmp = (a.size || 0) - (b.size || 0);
        else cmp = String(a.created_at || '').localeCompare(String(b.created_at || ''));
        return sortOrder === 'asc' ? cmp : -cmp;
      });
  }, [files, searchQuery, selectedCategory, selectedTag, fileTags, sortBy, sortOrder]);

  const totalBytes = useMemo(
    () => (files || []).reduce((acc, f) => acc + (Number(f.size) || 0), 0),
    [files]
  );

  const viewProps = {
    files: filteredFiles,
    deepHash,
    fileTags,
    getFileIcon,
    openTagEditor,
    setPreviewFile,
    setInspectedFile,
    onDownload,
    openCastModal,
    onCopyDeepLink,
    copiedHash,
    onShare,
    onDelete,
    onContextMenu: handleContextMenu,
  };

  return (
    <div
      className="p-4 sm:p-8 overflow-y-auto overflow-x-hidden h-full max-w-full relative"
      onContextMenu={handleContainerContextMenu}
      onDragOver={(e) => {
        e.preventDefault();
        if (!peerSession?.client) setIsDraggingOver(true);
      }}
      onDragLeave={() => setIsDraggingOver(false)}
      onDrop={handleDrop}
    >
      {isDraggingOver && (
        <div className="fixed inset-4 z-30 rounded-2xl border-2 border-dashed border-brand-400 bg-brand-500/15 backdrop-blur-xs flex items-center justify-center pointer-events-none">
          <div className="px-6 py-4 rounded-xl bg-surface-card border border-brand-400/40 text-center shadow-2xl">
            <p className="text-base font-semibold text-brand-200">📥 释放鼠标立即上传至网盘</p>
            <p className="text-xs text-gray-400 mt-1">自动计算 SHA-256 指纹并执行内容寻址去重</p>
          </div>
        </div>
      )}

      <div className="max-w-5xl mx-auto">
        <DriveToolbar
          sortBy={sortBy}
          setSortBy={setSortBy}
          sortOrder={sortOrder}
          setSortOrder={setSortOrder}
          viewMode={viewMode}
          setViewMode={setViewMode}
          busy={busy}
          onPick={onPick}
          fileRef={fileRef}
          fileCount={files?.length ?? 0}
          totalBytes={fmtBytes(totalBytes)}
          isRemotePeer={Boolean(peerSession?.client)}
          peerId={peerSession?.peerId}
        />

        {err && (
          <div className="mb-4 text-xs text-red-400 bg-red-400/10 border border-red-400/20 rounded-lg px-3 py-2">
            {err}
          </div>
        )}

        {shareLink && (
          <div className="mb-4 text-xs text-gray-300 bg-white/[0.04] border border-white/[0.08] rounded-lg px-3 py-2 break-all">
            Share link: <span className="font-mono text-brand-300">{shareLink}</span>
            <button onClick={() => navigator.clipboard?.writeText(shareLink)} className="ml-2 text-gray-500 hover:text-white">Copy</button>
          </div>
        )}

        <DriveCategoryBar
          selectedCategory={selectedCategory}
          setSelectedCategory={setSelectedCategory}
          searchQuery={searchQuery}
          setSearchQuery={setSearchQuery}
          selectedTag={selectedTag}
          setSelectedTag={setSelectedTag}
          allTagsWithCounts={allTagsWithCounts}
        />

        <DataState
          loading={files === null}
          error={err}
          empty={filteredFiles.length === 0}
          onRetry={load}
          skeletonType={viewMode === 'grid' ? 'cards' : 'table'}
          skeletonRows={8}
          emptyProps={{
            icon: '☁️',
            title: !peerSession?.client && files?.length === 0
              ? 'No connected node'
              : files?.length === 0 ? 'No files available' : 'No matching files found',
            description: !peerSession?.client && files?.length === 0
              ? 'Connect to an online node in Plaza to access files, or connect a local backend.'
              : files?.length === 0
                ? (peerSession?.client ? 'This remote node has not shared any public files yet.' : 'Click "Upload File" in the top right or drag & drop files anywhere on this page.')
                : 'Try adjusting your search query, category, or tag filter.',
            actionLabel: !peerSession?.client && files?.length === 0
              ? 'Find & Connect Node'
              : (files?.length === 0 && !peerSession?.client ? '+ Upload File' : null),
            onAction: !peerSession?.client && files?.length === 0
              ? () => navigate('/')
              : () => fileRef.current?.click(),
          }}
        >
          {viewMode === 'grid' ? (
            <DriveGridView {...viewProps} />
          ) : (
            <DriveListView
              {...viewProps}
              onApproveInbox={onApproveInbox}
              onRejectInbox={onRejectInbox}
            />
          )}
        </DataState>

        <TagEditorModal
          file={editingTagFile}
          tagInputText={tagInputText}
          setTagInputText={setTagInputText}
          onSave={saveTags}
          onClose={() => setEditingTagFile(null)}
        />

        <FileInspectorDrawer
          file={inspectedFile}
          tags={inspectedFile ? (fileTags[inspectedFile.hash] || []) : []}
          icon={inspectedFile ? getFileIcon(inspectedFile) : '📄'}
          copiedHash={copiedHash}
          onClose={() => setInspectedFile(null)}
          onPreview={(f) => setPreviewFile(f)}
          onDownload={(f) => onDownload(f)}
          onCopyDeepLink={(f) => onCopyDeepLink(f)}
          onShare={(f) => onShare(f)}
          onOpenTagEditor={(f) => openTagEditor(f)}
          onOpenCastModal={(f) => openCastModal(f)}
          onDelete={(f) => onDelete(f)}
        />

        {previewFile && (
          <FilePreviewModal
            file={previewFile}
            fetchBlob={async (hash) => {
              const effectiveType = mimeOf(previewFile.filename, previewFile.mime_type) || 'application/octet-stream';
              if (peerSession?.client) {
                const chunks = [];
                for await (const chunk of peerSession.client.stream(hash)) chunks.push(chunk);
                return new Blob(chunks, { type: effectiveType });
              }
              const u8 = await ws.download(hash);
              return new Blob([u8], { type: effectiveType });
            }}
            onClose={() => setPreviewFile(null)}
            onDownload={(f) => onDownload(f)}
          />
        )}

        <CastModal
          castFile={castFile}
          screens={screens}
          targetScreenId={targetScreenId}
          setTargetScreenId={setTargetScreenId}
          targetChannel={targetChannel}
          setTargetChannel={setTargetChannel}
          castStatus={castStatus}
          busy={busy}
          handleCast={handleCast}
          handleCastControl={handleCastControl}
          onClose={() => setCastFile(null)}
        />

        {contextMenu && (
          <ContextMenu
            x={contextMenu.x}
            y={contextMenu.y}
            onClose={() => setContextMenu(null)}
            items={buildDriveContextMenuItems({
              file: contextMenu.file,
              setPreviewFile,
              onDownload,
              openCastModal,
              onCopyDeepLink,
              onShare,
              openTagEditor,
              onDelete,
              fileRef,
              load,
            })}
          />
        )}
      </div>
    </div>
  );
}
