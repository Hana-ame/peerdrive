import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, act, waitFor } from '@testing-library/react';
import { MemoryRouter, Routes, Route } from 'react-router-dom';

import * as api from '../src/api';

// setup.js 的全局 mock 用的是手写 src/__mocks__/api.js（普通函数：既不能断言
// 调用参数，也不能注入返回值）。本文件要验证"点保存到底给后端发了什么"，所以在
// 文件级覆盖成 vi.fn() 版——只覆盖本文件，其它测试文件不受影响。
vi.mock('../src/api.js', () => ({
  getNodeMarket: vi.fn(),
  getJoinedNodes: vi.fn(),
  joinNode: vi.fn(),
  leaveNode: vi.fn(),
  getPeerShares: vi.fn(),
  getPullJobs: vi.fn(),
  startPull: vi.fn(),
  startPullCollection: vi.fn(),
  cancelPull: vi.fn(),
  listFiles: vi.fn(),
  listAnonCollections: vi.fn(),
  uploadFile: vi.fn(),
  downloadFileToDisk: vi.fn(),
  getBlobUrl: vi.fn(),
  deleteFile: vi.fn(),
  getShareScope: vi.fn(),
  setShareScope: vi.fn(),
  setFilesShared: vi.fn(),
}));

import {
  formatSize, formatTime, formatRelative, shortPeer, shortHash, baseName,
} from '../src/components/netdisk/format';
import FileTable, { Btn } from '../src/components/netdisk/FileTable';
import NodeCard from '../src/components/netdisk/NodeCard';
import TransferRow from '../src/components/netdisk/TransferRow';
import Market from '../src/pages/Market';
import PeerDetail from '../src/pages/PeerDetail';
import Transfers from '../src/pages/Transfers';
import Drive from '../src/pages/Drive';

// 网盘链路（M4）的界面契约测试。
// 重点不在"渲染出像素"，而在几条容易静默写错、且错了之后用户完全看不出问题的规则：
//   · formatSize 用十进制（KB/MB 1000 进制）——和 Finder/网盘习惯一致
//   · 对端未声明大小时不能画百分比进度条（假装有进度是骗人）
//   · "保存选中"必须把 hash + 相对路径都传给后端（丢 path 会让合集条目散落根目录）
//   · 传输页只在有 running 任务时轮询（否则长期空转打后端）
//   · 「共享」是独立于「持有」的选择：勾一个文件发的是 hash 列表，不是整份范围

beforeEach(() => {
  vi.clearAllMocks();
});

describe('netdisk/format', () => {
  it('formatSize 十进制进位，非法值给占位符', () => {
    expect(formatSize(0)).toBe('0 B');
    expect(formatSize(999)).toBe('999 B');
    expect(formatSize(1000)).toBe('1.0 KB');
    expect(formatSize(1500000)).toBe('1.5 MB');
    expect(formatSize(2000000000)).toBe('2.0 GB');
    // 负数/NaN/undefined 不该出现 NaN B 这种鬼东西
    expect(formatSize(-1)).toBe('—');
    expect(formatSize(undefined)).toBe('—');
    expect(formatSize('abc')).toBe('—');
  });

  it('formatTime 同时吃 ISO 字符串、Unix 秒与毫秒', () => {
    expect(formatTime('')).toBe('—');
    expect(formatTime('not-a-date')).toBe('—');
    // Unix 秒（后端 last_seen 的单位）与毫秒要落在同一时刻
    const iso = formatTime('2026-01-02T03:04:05Z');
    const sec = formatTime(Math.floor(Date.parse('2026-01-02T03:04:05Z') / 1000));
    const ms = formatTime(Date.parse('2026-01-02T03:04:05Z'));
    expect(sec).toBe(iso);
    expect(ms).toBe(iso);
  });

  it('formatRelative 给中文相对时间', () => {
    expect(formatRelative(null)).toBe('—');
    expect(formatRelative(Date.now() - 3 * 1000)).toBe('3 秒前');
    expect(formatRelative(Date.now() - 5 * 60 * 1000)).toBe('5 分钟前');
    expect(formatRelative(Date.now() - 3 * 3600 * 1000)).toBe('3 小时前');
    expect(formatRelative(Date.now() - 2 * 86400 * 1000)).toBe('2 天前');
  });

  it('shortPeer 保留头尾，短 id 原样返回', () => {
    expect(shortPeer('peerdrive-1a2b3c4d', 12)).toBe('peerdrive-1a2b3c4d');
    const long = 'peerdrive-' + 'a'.repeat(60);
    const out = shortPeer(long, 10);
    expect(out).toContain('…');
    expect(out.startsWith('peerdrive-')).toBe(true);
  });

  it('shortHash / baseName', () => {
    expect(shortHash('', 12)).toBe('');
    expect(shortHash('abcdef')).toBe('abcdef');
    expect(shortHash('a'.repeat(64))).toBe('aaaaaaaaaaaa…');
    expect(baseName('dir/sub/a.txt')).toBe('a.txt');
    expect(baseName('C:\\dir\\b.txt')).toBe('b.txt');
    expect(baseName('plain.txt')).toBe('plain.txt');
  });
});

describe('netdisk/FileTable', () => {
  const rows = [
    { key: 'h1', name: 'a.png', hash: 'h1', size: 1500, mime: 'image/png' },
    { key: 'h2', name: 'b.mp4', hash: 'h2', size: 2000, mime: 'video/mp4' },
  ];

  it('渲染行并可点击打开', () => {
    const onOpen = vi.fn();
    render(<FileTable rows={rows} onOpen={onOpen} />);
    expect(screen.getByText('a.png')).toBeTruthy();
    fireEvent.click(screen.getByText('b.mp4'));
    expect(onOpen).toHaveBeenCalledWith(rows[1]);
  });

  it('空列表显示占位文案而不是空表格', () => {
    render(<FileTable rows={[]} empty="对方没有共享文件" />);
    expect(screen.getByText('对方没有共享文件')).toBeTruthy();
  });

  it('加载态优先于空态', () => {
    render(<FileTable rows={[]} loading empty="不该出现" />);
    expect(screen.queryByText('不该出现')).toBeNull();
  });

  it('selectable 时勾选回调带上行 key', () => {
    const onToggleSelect = vi.fn();
    render(<FileTable rows={rows} selectable selected={['h2']} onToggleSelect={onToggleSelect} />);
    // 已选中的那一行 checkbox 必须是 checked（选中状态从父组件回灌）
    expect(screen.getByLabelText('选择 b.mp4').checked).toBe(true);
    fireEvent.click(screen.getByLabelText('选择 a.png'));
    expect(onToggleSelect).toHaveBeenCalledWith('h1');
  });

  it('Btn 支持禁用与色板', () => {
    const onClick = vi.fn();
    render(<Btn tone="primary" disabled onClick={onClick}>保存</Btn>);
    const btn = screen.getByText('保存');
    expect(btn.disabled).toBe(true);
    fireEvent.click(btn);
    expect(onClick).not.toHaveBeenCalled();
  });
});

describe('netdisk/NodeCard', () => {
  const node = { peer_id: 'peerdrive-abcdef123456', node_type: 'go-persistent', online: true, connected: true, joined: false, last_seen: Math.floor(Date.now() / 1000), shares: { collections: 2, files: 3 } };

  it('未加入时给「加入」按钮', () => {
    const onJoin = vi.fn();
    render(<NodeCard node={node} onJoin={onJoin} />);
    expect(screen.getByText('已直连')).toBeTruthy();
    expect(screen.getByText(/共享/)).toBeTruthy();
    fireEvent.click(screen.getByText('加入'));
    expect(onJoin).toHaveBeenCalledWith(node);
  });

  it('已加入时给「移出」按钮', () => {
    const onLeave = vi.fn();
    render(<NodeCard node={{ ...node, joined: true }} onLeave={onLeave} />);
    fireEvent.click(screen.getByText('移出'));
    expect(onLeave).toHaveBeenCalledWith(expect.objectContaining({ joined: true }));
  });

  it('离线节点不显示"最近在线"', () => {
    render(<NodeCard node={{ ...node, online: false, connected: false }} />);
    expect(screen.getByText('离线')).toBeTruthy();
    expect(screen.getByText('当前离线')).toBeTruthy();
  });
});

describe('netdisk/TransferRow', () => {
  const base = { id: 'j1', peer: 'peerdrive-xyz', hash: 'a'.repeat(64), name: 'f.bin', received: 500, total: 1000, status: 'running' };

  it('已知总量时显示百分比', () => {
    render(<TransferRow job={base} />);
    expect(screen.getByText('下载中')).toBeTruthy();
    expect(screen.getByText(/50%/)).toBeTruthy();
  });

  it('总量未知（-1）时不显示百分比，只报已收字节', () => {
    // 后端 fetchReader.Total 用 -1 表示对端没声明大小；此时画进度条等于编造
    render(<TransferRow job={{ ...base, total: -1 }} />);
    expect(screen.queryByText(/%/)).toBeNull();
    expect(screen.getByText('500 B')).toBeTruthy();
  });

  it('运行中才有取消按钮，结束时展示落盘路径与错误', () => {
    const onCancel = vi.fn();
    const { unmount } = render(<TransferRow job={base} onCancel={onCancel} />);
    fireEvent.click(screen.getByText('取消'));
    expect(onCancel).toHaveBeenCalled();
    unmount();

    render(<TransferRow job={{ ...base, status: 'done', saved_to: '/data/pulled/f.bin' }} />);
    expect(screen.queryByText('取消')).toBeNull();
    expect(screen.getByText(/已保存到/)).toBeTruthy();

    render(<TransferRow job={{ ...base, status: 'failed', error: 'sha256 mismatch' }} />);
    expect(screen.getByText('sha256 mismatch')).toBeTruthy();
  });

  it('本地已有同内容时打「本地已有」标', () => {
    render(<TransferRow job={{ ...base, status: 'done', skipped: true }} />);
    expect(screen.getByText('本地已有')).toBeTruthy();
  });
});

describe('pages/Market', () => {
  const marketNode = (over = {}) => ({
    peer_id: 'peerdrive-peer1', node_type: 'go-persistent', online: true, connected: false,
    joined: false, last_seen: Math.floor(Date.now() / 1000), shares: { collections: 1, files: 0 }, ...over,
  });

  const renderMarket = async (res) => {
    api.getNodeMarket.mockResolvedValue(res);
    const utils = render(<MemoryRouter><Market /></MemoryRouter>);
    // 侧栏里也有「节点市场」链接，用标题角色定位，避免多命中
    await screen.findByRole('heading', { name: '节点市场' });
    return utils;
  };

  it('展示本节点身份与市场条目', async () => {
    await renderMarket({ self: { peer_id: 'self-node', shares: { collections: 1, files: 2 } }, nodes: [marketNode()] });
    expect(await screen.findByText('self-node')).toBeTruthy();
    expect(screen.getByText(/共享 1 个合集 · 2 个文件/)).toBeTruthy();
    expect(screen.getByText('peerdrive-peer1')).toBeTruthy();
  });

  it('点加入调用后端并重新拉列表', async () => {
    await renderMarket({ self: { peer_id: 'self-node', shares: {} }, nodes: [marketNode()] });
    api.joinNode.mockResolvedValue({});
    // 页面底部还有一个「手动加入」表单也有「加入」按钮，取卡片里那个（第一个）
    fireEvent.click((await screen.findAllByRole('button', { name: '加入' }))[0]);
    // 加入是"写操作"：必须落到后端（本地乐观更新会让重启后清单不一致）
    expect(api.joinNode).toHaveBeenCalledWith('peerdrive-peer1');
    // onJoin 里是 await join → await load，断言要等这一串 promise 走完
    await waitFor(() => expect(api.getNodeMarket).toHaveBeenCalledTimes(2));
  });

  it('未开启共享时明确提示原因，而不是显示成 0 个', async () => {
    await renderMarket({ self: { peer_id: 'self-node', shares: { collections: 0, files: 0 } }, nodes: [] });
    expect(await screen.findByText(/未开启对外共享/)).toBeTruthy();
  });

  it('发现服务器不可达时不白屏，给出错误与空态', async () => {
    api.getNodeMarket.mockRejectedValue(new Error('discovery unreachable'));
    render(<MemoryRouter><Market /></MemoryRouter>);
    expect(await screen.findByText('discovery unreachable')).toBeTruthy();
    expect(screen.getByText(/当前没有发现其它节点/)).toBeTruthy();
  });

  it('只看已加入筛选生效', async () => {
    await renderMarket({ self: { peer_id: 'self-node', shares: {} }, nodes: [marketNode(), marketNode({ peer_id: 'peerdrive-peer2', joined: true })] });
    await screen.findByText('peerdrive-peer1');
    fireEvent.click(screen.getByRole('checkbox'));
    expect(screen.queryByText('peerdrive-peer1')).toBeNull();
    expect(screen.getByText('peerdrive-peer2')).toBeTruthy();
  });
});

describe('pages/PeerDetail', () => {
  const shares = {
    peer: 'peerdrive-peer1',
    collections: [
      { hash: 'c'.repeat(64), name: '打包合集', entries: [{ path: 'docs/a.txt', hash: 'h-a', mime: 'text/plain' }] },
    ],
    files: [{ name: 'b.bin', hash: 'h-b', path: 'downloads/b.bin', size: 1234 }],
  };

  // PeerDetail 用 useParams 取 peer，必须在真正的 Route 下渲染（MemoryRouter
  // 单独用不给 params，peer 会是 undefined，断言就会拿不到对端 id）。
  const renderDetail = async () => {
    api.getPeerShares.mockResolvedValue(shares);
    render(
      <MemoryRouter initialEntries={['/peers/peerdrive-peer1']}>
        <Routes>
          <Route path="/peers/:peer" element={<PeerDetail />} />
        </Routes>
      </MemoryRouter>
    );
    await screen.findByText('打包合集');
  };

  it('渲染对方的合集与单文件（即"文件链接"）', async () => {
    await renderDetail();
    expect(screen.getByText('b.bin')).toBeTruthy();
    expect(screen.getByText('1 个合集 · 1 个文件')).toBeTruthy();
  });

  it('保存单文件时把 hash 与相对路径一起传给后端', async () => {
    await renderDetail();
    api.startPull.mockResolvedValue({});
    fireEvent.click(screen.getByLabelText('选择 b.bin'));
    fireEvent.click(screen.getByText(/保存选中 \(1\)/));
    await screen.findByText('已创建 1 个保存任务');
    // path 必须带：丢了 path 合集条目会全部散落到下载根目录
    expect(api.startPull).toHaveBeenCalledWith('peerdrive-peer1', 'h-b', 'b.bin', 'downloads/b.bin');
  });

  it('合集条目先展开才能勾选，保存时带上条目路径', async () => {
    await renderDetail();
    api.startPull.mockResolvedValue({});
    // 折叠状态不渲染条目表格
    expect(screen.queryByLabelText('选择 docs/a.txt')).toBeNull();
    fireEvent.click(screen.getByText('▸'));
    fireEvent.click(await screen.findByLabelText('选择 docs/a.txt'));
    fireEvent.click(screen.getByText(/保存选中 \(1\)/));
    await screen.findByText('已创建 1 个保存任务');
    expect(api.startPull).toHaveBeenCalledWith('peerdrive-peer1', 'h-a', 'a.txt', 'docs/a.txt');
  });

  it('保存整个合集走批量端点', async () => {
    await renderDetail();
    api.startPullCollection.mockResolvedValue({ count: 3 });
    fireEvent.click(screen.getByText('保存整个合集'));
    await screen.findByText('已创建 3 个保存任务');
    expect(api.startPullCollection).toHaveBeenCalledWith('peerdrive-peer1', 'c'.repeat(64));
  });

  it('对方离线时给出可理解的错误', async () => {
    api.getPeerShares.mockRejectedValue(new Error('peer not connected'));
    render(
      <MemoryRouter initialEntries={['/peers/peerdrive-peer1']}>
        <Routes>
          <Route path="/peers/:peer" element={<PeerDetail />} />
        </Routes>
      </MemoryRouter>
    );
    expect(await screen.findByText('peer not connected')).toBeTruthy();
  });
});

describe('pages/Transfers', () => {
  const job = (over = {}) => ({ id: 'j1', peer: 'peerdrive-p', hash: 'h'.repeat(8), name: 'f.bin', received: 1, total: 10, status: 'running', ...over });

  it('进行中与已结束分组展示', async () => {
    api.getPullJobs.mockResolvedValue({ jobs: [job(), job({ id: 'j2', status: 'done' })] });
    render(<MemoryRouter><Transfers /></MemoryRouter>);
    expect(await screen.findByText('进行中')).toBeTruthy();
    expect(screen.getByText('已结束')).toBeTruthy();
  });

  it('空列表给出引导文案', async () => {
    api.getPullJobs.mockResolvedValue({ jobs: [] });
    render(<MemoryRouter><Transfers /></MemoryRouter>);
    expect(await screen.findByText(/还没有传输任务/)).toBeTruthy();
  });

  it('取消调用后端并刷新', async () => {
    api.getPullJobs.mockResolvedValue({ jobs: [job()] });
    api.cancelPull.mockResolvedValue({});
    render(<MemoryRouter><Transfers /></MemoryRouter>);
    fireEvent.click(await screen.findByText('取消'));
    expect(api.cancelPull).toHaveBeenCalledWith('j1');
    await waitFor(() => expect(api.getPullJobs).toHaveBeenCalledTimes(2));
  });

  it('有运行中的任务时按秒轮询', async () => {
    vi.useFakeTimers();
    try {
      api.getPullJobs.mockResolvedValue({ jobs: [job()] });
      render(<MemoryRouter><Transfers /></MemoryRouter>);
      await act(async () => { await vi.advanceTimersByTimeAsync(0); });
      const before = api.getPullJobs.mock.calls.length;
      await act(async () => { await vi.advanceTimersByTimeAsync(1200); });
      expect(api.getPullJobs.mock.calls.length).toBeGreaterThan(before);
    } finally {
      vi.useRealTimers();
    }
  });

  it('任务全部结束就停止轮询（不长期空转打后端）', async () => {
    vi.useFakeTimers();
    try {
      api.getPullJobs.mockResolvedValue({ jobs: [job({ status: 'done' })] });
      render(<MemoryRouter><Transfers /></MemoryRouter>);
      await act(async () => { await vi.advanceTimersByTimeAsync(0); });
      const before = api.getPullJobs.mock.calls.length;
      await act(async () => { await vi.advanceTimersByTimeAsync(10000); });
      expect(api.getPullJobs.mock.calls.length).toBe(before);
    } finally {
      vi.useRealTimers();
    }
  });
});

// 「自由选择共享内容」（doc/NETDISK.md M2.6）：
// 上传/登记进来只是"我持有"，对外提供是另一件事——逐行勾选，按 hash 增量改，
// 不整份重发（整份重发会让连点两下互相覆盖）。
describe('pages/Drive 共享勾选', () => {
  const H = 'a'.repeat(64);
  const files = [{ hash: H, filename: 'a.txt', size: 10 }];

  const scopeOf = (shared, level = 'public', friends = []) => ({
    enable: true,
    dirs: [],
    collections: [],
    friends,
    files: [{ hash: H, name: 'a.txt', size: 10, shared, by_dir: false, level }],
  });

  beforeEach(() => {
    api.listFiles.mockResolvedValue(files);
    api.listAnonCollections.mockResolvedValue([]);
    api.setFilesShared.mockResolvedValue({ enable: true, selected: [{ id: H, level: 'public' }] });
    api.setShareScope.mockResolvedValue({ enable: true, friends: [] });
  });

  it('未共享的文件显示「共享」，点了发 hash 列表给后端', async () => {
    api.getShareScope.mockResolvedValue(scopeOf(false));
    render(<MemoryRouter><Drive /></MemoryRouter>);
    fireEvent.click(await screen.findByText('共享'));
    // 第三参沿用当前级别：这是"共享/不共享"开关，不该顺手把级别改回 public
    expect(api.setFilesShared).toHaveBeenCalledWith([H], true, 'public');
  });

  it('已共享的文件显示「取消共享」，点了发 shared=false', async () => {
    api.getShareScope.mockResolvedValue(scopeOf(true));
    render(<MemoryRouter><Drive /></MemoryRouter>);
    fireEvent.click(await screen.findByText('取消共享'));
    expect(api.setFilesShared).toHaveBeenCalledWith([H], false, 'public');
  });

  it('总开关只发 enable 一个字段（不整份覆盖目录/文件选择）', async () => {
    api.getShareScope.mockResolvedValue(scopeOf(false));
    api.setShareScope.mockResolvedValue({ enable: false });
    render(<MemoryRouter><Drive /></MemoryRouter>);
    fireEvent.click(await screen.findByText('对外共享：开'));
    expect(api.setShareScope).toHaveBeenCalledWith({ enable: false });
  });

  it('共享范围拿不到（节点没开 peerjs）时不显示共享控件，文件列表照常', async () => {
    api.getShareScope.mockRejectedValue(new Error('503'));
    render(<MemoryRouter><Drive /></MemoryRouter>);
    expect(await screen.findByText('a.txt')).toBeTruthy();
    expect(screen.queryByText(/对外共享/)).toBeNull();
  });
});

// 共享级别（doc/NETDISK.md §12.6）：public 列出且可下载 / unlisted 不列出但可
// 下载 / private 只给自己与好友。界面上最容易写错的是"级别"与"共享"两件事的
// 耦合方式——没共享的东西不该出现级别下拉，否则用户会以为设了级别就等于共享了。
describe('pages/Drive 共享级别', () => {
  const H = 'b'.repeat(64);

  const scopeOf = (shared, level = 'public') => ({
    enable: true,
    dirs: [],
    collections: [],
    friends: [],
    files: [{ hash: H, name: 'b.txt', size: 3, shared, by_dir: false, level }],
  });

  beforeEach(() => {
    api.listFiles.mockResolvedValue([{ hash: H, filename: 'b.txt', size: 3 }]);
    api.listAnonCollections.mockResolvedValue([]);
    api.setFilesShared.mockResolvedValue({ enable: true, selected: [{ id: H, level: 'private' }] });
    api.setShareScope.mockResolvedValue({ enable: true, friends: ['pd-alpha'] });
  });

  // 注意：页面上还有"共享目录/好友"那几个下拉也带"公开"选项，所以这里必须按
  // 文件行的 aria-label 定位，不能只按显示值找（否则多元素匹配、断言假通过）。
  it('已共享的文件才显示级别下拉（没共享就没有"给谁看"这回事）', async () => {
    api.getShareScope.mockResolvedValue(scopeOf(false));
    render(<MemoryRouter><Drive /></MemoryRouter>);
    await screen.findByText('b.txt');
    expect(screen.queryByLabelText('b.txt 的共享级别')).toBeNull();
  });

  it('改级别：带上 level 且保持 shared=true', async () => {
    api.getShareScope.mockResolvedValue(scopeOf(true, 'public'));
    render(<MemoryRouter><Drive /></MemoryRouter>);
    const sel = await screen.findByLabelText('b.txt 的共享级别');
    fireEvent.change(sel, { target: { value: 'private' } });
    expect(api.setFilesShared).toHaveBeenCalledWith([H], true, 'private');
  });

  it('好友名单按逗号拆分后 PUT friends（不把空白项写进去）', async () => {
    api.getShareScope.mockResolvedValue(scopeOf(true, 'private'));
    render(<MemoryRouter><Drive /></MemoryRouter>);
    const input = await screen.findByPlaceholderText(/用逗号分隔/);
    fireEvent.change(input, { target: { value: ' pd-alpha , pd-beta ' } });
    fireEvent.click(screen.getByText('保存好友'));
    expect(api.setShareScope).toHaveBeenCalledWith({ friends: ['pd-alpha', 'pd-beta'] });
  });

  it('级别下拉回填当前级别而不是默认 public', async () => {
    api.getShareScope.mockResolvedValue(scopeOf(true, 'unlisted'));
    render(<MemoryRouter><Drive /></MemoryRouter>);
    const sel = await screen.findByLabelText('b.txt 的共享级别');
    expect(sel.value).toBe('unlisted');
  });
});

// 共享目录（doc/NETDISK.md §12）：整目录共享是最常用的粒度（"把我这个照片目录
// 给出去"），后端 PUT /peerjs/share 的 dirs 是**整体替换**——只发新增的那一条
// 会静默清掉其它目录，这是这里最容易写错、且用户事后才发现的地方。
describe('pages/Drive 共享目录', () => {
  const H = 'c'.repeat(64);

  const scopeOf = (dirs) => ({
    enable: true,
    dirs,
    collections: [],
    friends: [],
    files: [{ hash: H, name: 'c.txt', size: 3, shared: false, by_dir: false, level: 'public' }],
  });

  beforeEach(() => {
    api.listFiles.mockResolvedValue([{ hash: H, filename: 'c.txt', size: 3 }]);
    api.listAnonCollections.mockResolvedValue([]);
    api.setFilesShared.mockResolvedValue({ enable: true, selected: [] });
    // 后端会归一化（绝对路径/去重/排序），回的是完整新范围
    api.setShareScope.mockImplementation(async (patch) => ({
      enable: true, dirs: patch.dirs || [], friends: [],
    }));
  });

  it('添加目录时把已有目录一起发（dirs 是整体替换，不是追加）', async () => {
    api.getShareScope.mockResolvedValue(scopeOf([{ id: '/media', level: 'public' }]));
    render(<MemoryRouter><Drive /></MemoryRouter>);
    fireEvent.change(await screen.findByPlaceholderText(/目录绝对路径/), { target: { value: '/photos' } });
    fireEvent.click(screen.getByText('添加目录'));
    await waitFor(() => expect(api.setShareScope).toHaveBeenCalled());
    expect(api.setShareScope).toHaveBeenCalledWith({
      dirs: [{ id: '/media', level: 'public' }, { id: '/photos', level: 'public' }],
    });
  });

  it('新目录带上选中的级别，而不是一律 public', async () => {
    api.getShareScope.mockResolvedValue(scopeOf([]));
    render(<MemoryRouter><Drive /></MemoryRouter>);
    fireEvent.change(await screen.findByPlaceholderText(/目录绝对路径/), { target: { value: '/private' } });
    fireEvent.change(screen.getByLabelText('新目录的共享级别'), { target: { value: 'private' } });
    fireEvent.click(screen.getByText('添加目录'));
    await waitFor(() => expect(api.setShareScope).toHaveBeenCalled());
    expect(api.setShareScope).toHaveBeenCalledWith({ dirs: [{ id: '/private', level: 'private' }] });
  });

  it('移除一条只发剩下的目录（不是发空列表）', async () => {
    api.getShareScope.mockResolvedValue(scopeOf([
      { id: '/media', level: 'public' },
      { id: '/photos', level: 'unlisted' },
    ]));
    render(<MemoryRouter><Drive /></MemoryRouter>);
    fireEvent.click(await screen.findByText('/photos')); // 目录行渲染出路径本身
    const btns = screen.getAllByText('移除');
    fireEvent.click(btns[1]); // 第二条 = /photos
    await waitFor(() => expect(api.setShareScope).toHaveBeenCalled());
    expect(api.setShareScope).toHaveBeenCalledWith({ dirs: [{ id: '/media', level: 'public' }] });
  });

  it('改目录级别：整份列表回写，只改那一条的 level', async () => {
    api.getShareScope.mockResolvedValue(scopeOf([{ id: '/media', level: 'public' }]));
    render(<MemoryRouter><Drive /></MemoryRouter>);
    fireEvent.change(await screen.findByLabelText('/media 的共享级别'), { target: { value: 'unlisted' } });
    await waitFor(() => expect(api.setShareScope).toHaveBeenCalled());
    expect(api.setShareScope).toHaveBeenCalledWith({ dirs: [{ id: '/media', level: 'unlisted' }] });
  });

  it('空输入不发请求（别把空字符串写进共享范围）', async () => {
    api.getShareScope.mockResolvedValue(scopeOf([]));
    render(<MemoryRouter><Drive /></MemoryRouter>);
    fireEvent.click(await screen.findByText('添加目录'));
    expect(api.setShareScope).not.toHaveBeenCalled();
    expect(await screen.findByText('请填目录的绝对路径')).toBeTruthy();
  });
});
