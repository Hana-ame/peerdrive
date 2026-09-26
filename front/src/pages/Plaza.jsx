// 合集广场：浏览/搜索本机匿名合集和 P2P 公开合集
import React, { useState, useEffect } from 'react';
import { useNavigate } from 'react-router-dom';
import { listAnonCollections, listPublicCollections, getPeerjsNode } from '../api';
import CollectionCard from '../components/CollectionCard';
import PeerJSConnect from '../components/PeerJSConnect';

// 从文本中提取 SHA256 哈希
const SHA256_RE = /\b([a-f0-9]{64})\b/i;
function extractHash(text) { const m = (text || '').match(SHA256_RE); return m ? m[1].toLowerCase() : null; }

// 占位示例合集（无后端时展示）
const DUMMY_COLLECTIONS = [
  { hash: 'demo-1', friendly_name: '🧪 示例图片集', version: 1, isDummy: true, entries: [{ path: 'cat.jpg' }, { path: 'dog.png' }] },
  { hash: 'demo-2', friendly_name: '🧪 示例文档集', version: 2, isDummy: true, entries: [{ path: 'readme.md' }, { path: 'notes.txt' }] },
];

// 骨架屏组件：加载时的占位卡片
function SkeletonCard() {
  return (
    <div className="bg-surface-card border border-surface-border rounded-card p-5 animate-pulse">
      <div className="flex items-center justify-between mb-4">
        <div className="w-10 h-10 bg-white/[0.06] rounded-lg" />
        <div className="w-12 h-3 bg-white/[0.06] rounded" />
      </div>
      <div className="w-3/4 h-5 bg-white/[0.06] rounded mb-2" />
      <div className="w-1/2 h-3 bg-white/[0.06] rounded" />
    </div>
  );
}

export default function Plaza() {
  const [collections, setCollections] = useState([]);
  const [loading, setLoading] = useState(false);
  const [searchInput, setSearchInput] = useState('');
  const [p2pOnline, setP2pOnline] = useState(false);
  const [plazaTab, setPlazaTab] = useState('local');
  const [viewMode, setViewMode] = useState('grid');
  const navigate = useNavigate();

  // 首次加载时拉取合集列表和 P2P 状态
  useEffect(() => { loadAll(); getPeerjsNode().then(s => setP2pOnline(s?.online && (s?.peers?.length || 0) > 0)).catch(()=>{}); }, []);

  const loadAll = async () => {
    // 加载本机 + 公开合集
    setLoading(true);
    try {
      const [anon, pub] = await Promise.all([
        listAnonCollections().catch(() => []),
        listPublicCollections().catch(() => ({ collections: [] })),
      ]);
      const merged = [
        ...(Array.isArray(anon) ? anon.map(c => ({ ...c, _type: 'anon' })) : []),
        ...((pub.collections || pub.data || []).map(c => ({ ...c, _type: 'public' }))),
      ];
      setCollections(merged);
    } catch { setCollections([]); }
    setLoading(false);
  };

  // 搜索/跳转：输入 Hash 导航到合集
  const handleSearch = () => {
    const h = extractHash(searchInput);
    if (h && h.length === 64) navigate(`/anon/collections/${h}`);
    else if (searchInput.trim()) alert('请输入有效的 64 位 SHA256 Hash');
  };

  const collId = (c) => c.id || c.hash || c.collection_name;

  // 跳转到创建页并携带 Fork 源数据
  // 注意：公开合集来自 /collections/public，列表项没有 hash 字段，只有
  // current_hash；匿名合集列表才有 hash。两者都代表“合集内容快照”，创建副本
  // 必须取其一传给 AnonCreator 去按哈希拉全量条目。
  const handleFork = (c) => {
    const h = c.hash || c.current_hash;
    if (h) navigate('/create', { state: { forkFrom: c, sourceHash: h } });
  };

  // 点击合集卡片跳转到详情页
  const handleDownload = (c) => {
    if (c.isDummy) return;
    if (c.hash) navigate(`/anon/collections/${c.hash}`);
    else if (c.current_hash) navigate(`/anon/collections/${c.current_hash}`);
    else if (c.username && c.collection_name) navigate(`/${c.username}/${c.collection_name}`);
  };

  const showDummies = collections.length === 0 && !loading;
  const display = showDummies ? DUMMY_COLLECTIONS : collections;
  const localColls = display.filter(c => c._type === 'anon' || c.isDummy);
  const p2pColls = display.filter(c => c._type === 'public');
  // 天线 tab 已删除：BEP51 只能采样 torrent DHT 的 20 字节 infohash，
  // 永远取不到 peerdrive 合集 announce 的 hash，点击打开的闭环不可达（发现背景见 git log）
  const activeColls = plazaTab === 'p2p' ? p2pColls : localColls;

  return (
    <div className="p-8 overflow-y-auto h-full">
      <div className="max-w-6xl mx-auto">
        {/* 页面标题栏：标题 / P2P 状态 / 视图切换 / 创建按钮 */}
        <div className="flex items-center justify-between mb-4">
          <div className="flex items-center gap-3">
            <h1 className="text-3xl font-bold">合集</h1>
            {p2pOnline && <span className="badge-soft border-emerald-400/20 bg-emerald-400/10 text-emerald-300">P2P 在线</span>}
          </div>
          <div className="flex items-center gap-2">
            <button
              onClick={() => setViewMode(viewMode === 'grid' ? 'list' : 'grid')}
              className="btn-ghost"
              title={viewMode === 'grid' ? '切换为列表视图' : '切换为网格视图'}
            >
              {viewMode === 'grid' ? '≡ 列表' : '⊞ 网格'}
            </button>
            <button onClick={() => navigate('/create')} className="btn-brand">+ 创建合集</button>
          </div>
        </div>
        {/* 连接节点（PeerJS 拨号对端，消费端）——默认展开，首页即可用 */}
        <div className="mb-4">
          <div className="bg-white/[0.03] rounded-card border border-white/[0.06] p-4">
            <p className="text-xs text-gray-400 mb-2">
              节点搜索 / 连接（PeerJS）<span className="text-gray-600">——搜索在线节点、按 peer id 拨号连接，看对方共享的文件/合集并保存</span>
            </p>
            <PeerJSConnect compact />
          </div>
        </div>

        {/* 搜索栏：输入 Hash / URL 直接跳转 */}
        <div className="mb-6">
          <div className="flex gap-2">
            <input type="text" value={searchInput}
              onChange={e => { setSearchInput(e.target.value); }}
              onPaste={e => { const h = extractHash(e.clipboardData.getData('text')); if (h) { e.preventDefault(); setSearchInput(h); navigate(`/anon/collections/${h}`); } }}
              onKeyDown={e => { if (e.key === 'Enter') handleSearch(); }}
              placeholder="输入 SHA256 Hash / URL 打开合集"
              className="input-base flex-1 font-mono" />
            <button onClick={handleSearch} className="btn-brand">查看</button>
          </div>
          {/* 标签切换：本机 / P2P 网络 */}
          <div className="flex gap-1 mt-3">
            <button onClick={() => setPlazaTab('local')} className={`px-4 py-1.5 text-sm rounded ${plazaTab==='local'?'bg-brand-600 text-white shadow-glow':'bg-white/[0.04] text-gray-400 hover:text-white hover:bg-white/[0.08]'}`}>💻 本机 ({localColls.length})</button>
            <button onClick={() => setPlazaTab('p2p')} className={`px-4 py-1.5 text-sm rounded ${plazaTab==='p2p'?'bg-brand-600 text-white shadow-glow':'bg-white/[0.04] text-gray-400 hover:text-white hover:bg-white/[0.08]'}`}>🌐 P2P 网络 ({p2pColls.length})</button>
          </div>
        </div>

        {loading ? (
          <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-3 lg:grid-cols-4 gap-6">
            {Array.from({ length: 8 }).map((_, i) => <SkeletonCard key={i} />)}
          </div>
        ) : activeColls.length === 0 ? (
          <div className="text-center py-20 text-gray-500 border-2 border-dashed border-white/10 rounded-card">
            <p className="mb-3">{plazaTab === 'p2p' ? 'P2P 网络暂无公开合集' : '还没有创建任何合集'}</p>
            <p className="text-xs text-gray-600 mb-4">从文件管理器注册文件并自动创建匿名合集，或手动创建</p>
            <div className="flex justify-center gap-3">
              <button onClick={() => navigate('/create')} className="btn-brand">
                + 创建合集
              </button>
            </div>
          </div>
        ) : viewMode === 'list' ? (
          <div className="card-surface overflow-hidden">
            {activeColls.map(c => (
              <CollectionCard
                key={collId(c)}
                collection={c}
                viewMode="list"
                onFork={handleFork}
                onDownload={handleDownload}
              />
            ))}
          </div>
        ) : (
          <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-3 lg:grid-cols-4 gap-6">
            {activeColls.map(c => (
              <CollectionCard
                key={collId(c)}
                collection={c}
                viewMode="grid"
                onFork={handleFork}
                onDownload={handleDownload}
              />
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
