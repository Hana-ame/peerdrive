import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import App from '../src/App';
import Plaza from '../src/pages/Plaza';
import AnonCreator from '../src/pages/AnonCreator';
import AnonExplorer from '../src/pages/AnonExplorer';
import FileManager from '../src/pages/FileManager';
import Settings from '../src/pages/Settings';
import VersionLog from '../src/components/VersionLog';
import Navbar from '../src/components/Navbar';
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
    expect(screen.getByText('设置')).toBeTruthy();
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
    expect(screen.getByText('文件管理')).toBeTruthy();
    expect(screen.getByText('创建合集')).toBeTruthy();
  });
});

