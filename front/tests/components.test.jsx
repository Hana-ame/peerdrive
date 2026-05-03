import { describe, it, expect } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import App from '../src/App';
import Plaza from '../src/pages/Plaza';
import AnonCreator from '../src/pages/AnonCreator';
import AnonExplorer from '../src/pages/AnonExplorer';
import FileManager from '../src/pages/FileManager';
import Settings from '../src/pages/Settings';
import VersionLog from '../src/components/VersionLog';
import Navbar from '../src/components/Navbar';
import EditorToolbar from '../src/pages/AnonCreator/EditorToolbar';
import FileTree from '../src/components/FileTree';
const wrapper = ({ children }) => <MemoryRouter>{children}</MemoryRouter>;

describe('App', () => {
  it('renders Navbar with Peerdrive logo', () => {
    render(<App />);
    expect(screen.getByText('Peerdrive')).toBeTruthy();
  });
});

describe('Plaza', () => {
  it('renders title', () => {
    render(<Plaza />, { wrapper });
    expect(screen.getByText('合集')).toBeTruthy();
  });
  it('has create button', () => {
    render(<Plaza />, { wrapper });
    expect(screen.getByText('+ 创建合集')).toBeTruthy();
  });
  it('has search input', () => {
    render(<Plaza />, { wrapper });
    expect(screen.getByPlaceholderText(/SHA256|Hash|URL/)).toBeTruthy();
  });
});

describe('AnonCreator', () => {
  it('renders 4-tab bar', () => {
    render(<AnonCreator />, { wrapper });
    expect(screen.getByText(/所有文件/)).toBeTruthy();
    expect(screen.getByText(/已注册/)).toBeTruthy();
    expect(screen.getByText(/本地电脑/)).toBeTruthy();
    const allText = document.body.textContent;
    expect(allText).toContain('合集');
  });
  it('has save button', () => {
    render(<AnonCreator />, { wrapper });
    expect(screen.getByText(/保存/)).toBeTruthy();
  });
  it('has no Commit button', () => {
    render(<AnonCreator />, { wrapper });
    expect(screen.queryByText('Commit')).toBeNull();
  });
});

describe('AnonExplorer', () => {
  it('renders hash input', () => {
    render(<AnonExplorer />, { wrapper });
    expect(screen.getByPlaceholderText(/Hash|SHA/)).toBeTruthy();
  });
});

describe('FileManager', () => {
  it('renders title', () => {
    render(<FileManager />, { wrapper });
    expect(screen.getByText('文件管理')).toBeTruthy();
  });
});

describe('Settings', () => {
  it('renders page', () => {
    render(<Settings dataConsent={false} setDataConsent={() => {}} />, { wrapper });
    expect(screen.getAllByText('设置').length).toBeGreaterThanOrEqual(1);
  });
});

describe('EditorToolbar', () => {
  it('保存按钮在无有效条目时禁用', () => {
    render(<EditorToolbar fname="" tags="" entryCount={0} validCount={0} saving={false}
      onFname={() => {}} onTags={() => {}} onSave={() => {}} />);
    const btn = screen.getByText(/保存/);
    expect(btn.disabled).toBe(true);
  });

  it('保存按钮在有有效条目时可用', () => {
    render(<EditorToolbar fname="" tags="" entryCount={1} validCount={1} saving={false}
      onFname={() => {}} onTags={() => {}} onSave={() => {}} />);
    const btn = screen.getByText(/保存/);
    expect(btn.disabled).toBe(false);
  });

  it('标签输入在独立行', () => {
    render(<EditorToolbar fname="" tags="" entryCount={0} validCount={0} saving={false}
      onFname={() => {}} onTags={() => {}} onSave={() => {}} />);
    expect(screen.getByPlaceholderText(/标签/)).toBeTruthy();
  });

  it('显示文件计数', () => {
    render(<EditorToolbar fname="" tags="" entryCount={5} validCount={3} saving={false}
      onFname={() => {}} onTags={() => {}} onSave={() => {}} />);
    expect(screen.getByText('3 个文件')).toBeTruthy();
  });
});

describe('FileTree', () => {
  it('空状态显示拖拽提示', () => {
    render(<FileTree entries={[]} entryActions={{}} />);
    expect(screen.getByText(/拖拽文件到此处/)).toBeTruthy();
  });

  it('新建文件夹按钮存在', () => {
    const entries = [{ path: 'test.txt', hash: 'a', name: 'test.txt' }];
    render(<FileTree entries={entries} entryActions={{}}
      onNewFolder={() => {}} />);
    expect(screen.getByText(/新建文件夹/)).toBeTruthy();
  });

  it('点击新建文件夹显示内联输入框', () => {
    const entries = [{ path: 'test.txt', hash: 'a', name: 'test.txt' }];
    render(<FileTree entries={entries} entryActions={{}} />);
    fireEvent.click(screen.getByText(/新建文件夹/));
    expect(screen.getByPlaceholderText('文件夹名称')).toBeTruthy();
  });
});

describe('VersionLog', () => {
  it('renders empty state', () => {
    render(<VersionLog username="" collName="" triggerRefresh={0} />);
    expect(screen.getByText('版本历史')).toBeTruthy();
    expect(screen.getByText('暂无版本')).toBeTruthy();
  });
});

describe('Navbar', () => {
  it('renders nav links', () => {
    render(<Navbar />, { wrapper });
    expect(screen.getByText('本地文件管理')).toBeTruthy();
    expect(screen.getByText('创建合集')).toBeTruthy();
    expect(screen.getByText('BT')).toBeTruthy();
    expect(screen.getByText('IPFS')).toBeTruthy();
  });
});

