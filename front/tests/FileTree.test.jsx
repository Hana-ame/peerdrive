import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';
import FileTree from '../src/components/FileTree';

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
});
