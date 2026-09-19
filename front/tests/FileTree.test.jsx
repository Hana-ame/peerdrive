import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';
import FileTree, { buildFlatTree } from '../src/components/FileTree';

describe('FileTree', () => {
  it('renders empty state', () => {
    const { container } = render(<FileTree entries={[]} entryActions={{}} />);
    expect(container.textContent).toContain('拖拽');
  });

  it('renders flat entries', () => {
    const entries = [{ path: 'a.txt', hash: 'abc123', size: 100, mime_type: 'text/plain' }];
    const { container } = render(<FileTree entries={entries} entryActions={{}} />);
    expect(container.textContent).toContain('a.txt');
    expect(container.textContent).toContain('条目');
  });

  it('shows folder structure for nested paths', () => {
    const entries = [{ path: 'd/a.txt', hash: 'abc', size: 10, mime_type: '' }];
    const { container } = render(<FileTree entries={entries} entryActions={{}} />);
    expect(container.textContent).toContain('d');
  });

  it('renders new folder button', () => {
    const { container } = render(<FileTree entries={[{ path: 'f.txt', hash: 'abc', size: 0, mime_type: '' }]} entryActions={{}} />);
    expect(container.textContent).toContain('新建文件夹');
  });

  // 发现背景：用户反馈「新建文件夹是 broken 的」——根源是空合集时 FileTree 提前 return
  // 占位提示，工具栏（含新建文件夹按钮）根本没渲染，导致从零建合集时无法先建目录结构。
  it('renders toolbar also when there are no entries', () => {
    const { container } = render(<FileTree entries={[]} entryActions={{}} />);
    expect(container.textContent).toContain('新建文件夹');
    expect(container.textContent).toContain('0 条目');
  });

  // 发现背景：AnonCollectionEntry 的 MIME 只在 providers[].mime_type，
  // 旧实现只读顶层 e.mime_type，合集条目在 FileTree 里图标永远回到默认 📄。
  it('derives mime from providers when top-level mime_type is missing', () => {
    const tree = buildFlatTree([
      { path: 'pic.png', hash: 'abc', size: 1, providers: [{ type: 'sha256', value: 'abc', mime_type: 'image/png' }] },
    ]);
    expect(tree[0].mime_type).toBe('image/png');
  });
});
