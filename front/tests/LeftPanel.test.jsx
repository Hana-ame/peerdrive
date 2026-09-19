import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import LeftPanel from '../src/pages/AnonCreator/LeftPanel';

// LeftPanel 需要大量 props，这里给一份最小可用桩。
const baseProps = {
  sourceTab: 'registered',
  sortKey: 'created_at',
  sortOrder: 'desc',
  typeFilters: [],
  files: [],
  filteredFiles: [],
  search: '',
  collections: [],
  filteredCollections: [],
  collSearch: '',
  collTagFilter: [],
  collSort: 'time',
  allCollTags: [],
  enteredColl: null,
  collViewPath: '',
  enteredCollFiles: null,
  selectMode: false,
  selectedColls: new Set(),
  selectedFiles: new Set(),
  sysPath: '/',
  sysEntries: [],
  sysLoading: false,
  regDirPath: '',
  searchHistory: [],
  ...Object.fromEntries([
    'onSourceTab', 'onSortKey', 'onSortOrder', 'onTypeFilter', 'onSearch',
    'onFileSelect', 'onFileAdd', 'onDragStart', 'onCollSearch', 'onCollTag', 'onCollSort',
    'onEnterColl', 'onLeaveColl', 'onPathNav', 'onNavIntoDir', 'onSaveToNode', 'onSelectToggle',
    'onToggleCollSelect', 'onToggleFileSelect', 'onBatchSaveColls', 'onBatchSaveFiles',
    'onSysNav', 'onSysAdd', 'onSysAddFile', 'onSysAddFolder', 'onRegDirPath',
    'onSearchHistorySelect', 'onSearchHistoryShow', 'onSearchHistoryUpdate',
  ].map(k => [k, vi.fn()])),
};

describe('LeftPanel 文件视图', () => {
  // 发现背景：SOURCE_TABS 曾被删到只剩「本地电脑 / 合集」，LeftPanel 的另外两个分支
  // 只剩注释、列表区什么都不渲染 —— 用户看到的现象就是「已注册（按目录）是空的」。
  it('提供四个来源标签，包含两个已注册视图', () => {
    render(<LeftPanel {...baseProps} />);
    expect(screen.getByTitle('本地电脑')).toBeTruthy();
    expect(screen.getByTitle('已注册·按文件')).toBeTruthy();
    expect(screen.getByTitle('已注册·按目录')).toBeTruthy();
    expect(screen.getByTitle('合集')).toBeTruthy();
  });

  it('已注册·按文件：渲染文件名而非空白面板', () => {
    const files = [
      { hash: 'h1', filename: 'report.pdf', size: 1024, mime_type: 'application/pdf', created_at: '2026-09-01T00:00:00Z', provider_path: '/data/report.pdf' },
    ];
    render(<LeftPanel {...baseProps} files={files} filteredFiles={files} />);
    expect(screen.getByText('report.pdf')).toBeTruthy();
  });

  // 发现背景：本地搜索框原先只过滤「已注册」列表，在本机目录树里打字毫无反应
  // —— 用户反馈「本地电脑的搜索框不是搜索本地电脑的」。
  it('本地电脑：搜索框过滤的是本机目录条目', () => {
    const props = {
      ...baseProps,
      sourceTab: 'local',
      search: 'img',
      sysEntries: [
        { name: 'img001.png', path: '/img001.png', is_dir: false, size: 10 },
        { name: 'notes.txt', path: '/notes.txt', is_dir: false, size: 10 },
      ],
    };
    render(<LeftPanel {...props} />);
    expect(screen.getByText('img001.png')).toBeTruthy();
    expect(screen.queryByText('notes.txt')).toBeNull();
  });

  // 发现背景：按目录视图过去是空 div。这里验证它会按 provider_path 顶层目录分组，
  // 并且点击目录会走 onDirPath 下钻回调。
  it('已注册·按目录：按目录分组并支持下钻', () => {
    const files = [
      { hash: 'h2', filename: 'a.txt', size: 1, mime_type: 'text/plain', created_at: '2026-09-02T00:00:00Z', provider_path: '/srv/media/a.txt' },
    ];
    // prop 名是 onRegDirPath（下钻回调），别写成 View 内部的 onDirPath。
    const onRegDirPath = vi.fn();
    render(<LeftPanel {...baseProps} sourceTab="registered_dir" files={files} filteredFiles={files} onRegDirPath={onRegDirPath} />);
    expect(screen.getByText('srv')).toBeTruthy();
    fireEvent.click(screen.getByText('srv'));
    expect(onRegDirPath).toHaveBeenCalledWith('srv');
  });
});
