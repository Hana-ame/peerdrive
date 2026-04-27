// 合集广场：浏览/搜索本机匿名合集和 P2P 公开合集，支持 Fork/分享/下载
import React, { useState, useEffect } from 'react';
import { useNavigate } from 'react-router-dom';
import { listAnonCollections, listPublicCollections, getP2PStatus, createShare, getShareUrl } from '../api';
import CollectionCard from '../components/CollectionCard';

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
    <div className="bg-gray-800 border border-gray-700 rounded-xl p-5 animate-pulse">
      <div className="flex items-center justify-between mb-4">
        <div className="w-10 h-10 bg-gray-700 rounded-lg" />
        <div className="w-12 h-3 bg-gray-700 rounded" />
      </div>
      <div className="w-3/4 h-5 bg-gray-700 rounded mb-2" />
      <div className="w-1/2 h-3 bg-gray-700 rounded" />
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
  useEffect(() => { loadAll(); getP2PStatus().then(s => setP2pOnline(s?.enabled && s?.connected_count > 0)).catch(()=>{}); }, []);

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

  // 搜索/跳转：输入 Hash 或关键字导航到合集
  const handleSearch = () => {
    const h = extractHash(searchInput) || searchInput.trim().toLowerCase();
    if (h && h.length === 64) navigate(`/anon/collections/${h}`);
    else if (h) navigate(`/anon/collections/${h}`);
  };

  const collId = (c) => c.id || c.hash || c.collection_name;

  const [showShare, setShowShare] = useState(null); // {url, name}

  // 生成分享链接并复制到剪贴板
  const handleShare = async (c) => {
    try {
      const share = await createShare(c.hash || c.id, 'collection', c.friendly_name || c.name_preview || '合集');
      const url = getShareUrl(share.token);
      setShowShare({ url, name: c.friendly_name || c.name_preview || '合集' });
      navigator.clipboard.writeText(url).catch(()=>{});
    } catch(e) { alert('分享失败: ' + e.message); }
  };

  // 跳转到创建页并携带 Fork 源数据
  const handleFork = (c) => {
    if (c.hash) navigate('/anon/create', { state: { forkFrom: c, sourceHash: c.hash } });
  };

  // 点击合集卡片跳转到详情页
  const handleDownload = (c) => {
    if (c.isDummy) return;
    if (c.hash) navigate(`/anon/collections/${c.hash}`);
    else if (c.username && c.collection_name) navigate(`/${c.username}/${c.collection_name}`);
  };

  const showDummies = collections.length === 0 && !loading;
  const display = showDummies ? DUMMY_COLLECTIONS : collections;
  const localColls = display.filter(c => c._type === 'anon' || c.isDummy);
  const p2pColls = display.filter(c => c._type === 'public');
  const activeColls = plazaTab === 'p2p' ? p2pColls : localColls;

  return (
    <div className="p-8 overflow-y-auto h-full">
      <div className="max-w-6xl mx-auto">
        {/* 页面标题栏：标题 / P2P 状态 / 视图切换 / 创建按钮 */}
        <div className="flex items-center justify-between mb-4">
          <div className="flex items-center gap-3">
            <h1 className="text-3xl font-bold">合集</h1>
            {p2pOnline && <span className="text-xs bg-emerald-900/50 text-emerald-400 px-2 py-0.5 rounded-full">P2P 在线</span>}
          </div>
          <div className="flex items-center gap-2">
            <button
              onClick={() => setViewMode(viewMode === 'grid' ? 'list' : 'grid')}
              className="bg-gray-800 hover:bg-gray-700 text-gray-400 px-3 py-2 rounded-lg text-sm border border-gray-700"
              title={viewMode === 'grid' ? '切换为列表视图' : '切换为网格视图'}
            >
              {viewMode === 'grid' ? '≡ 列表' : '⊞ 网格'}
            </button>
            <button onClick={() => navigate('/anon/create')} className="bg-blue-600 hover:bg-blue-700 text-white px-4 py-2 rounded text-sm">+ 创建合集</button>
          </div>
        </div>
        {/* 搜索栏：输入 Hash / URL 直接跳转 */}
        <div className="mb-6">
          <div className="flex gap-2">
            <input type="text" value={searchInput}
              onChange={e => { setSearchInput(e.target.value); const h = extractHash(e.target.value); if (h) { navigate(`/anon/collections/${h}`); } }}
              onPaste={e => { const h = extractHash(e.clipboardData.getData('text')); if (h) { e.preventDefault(); setSearchInput(h); navigate(`/anon/collections/${h}`); } }}
              onKeyDown={e => { if (e.key === 'Enter') handleSearch(); }}
              placeholder="输入 SHA256 Hash / URL 打开合集，或搜索 P2P 网络..."
              className="flex-1 bg-gray-800 border border-gray-600 px-4 py-2.5 rounded-lg text-sm font-mono focus:outline-none focus:border-blue-500" />
            <button onClick={handleSearch} className="bg-blue-600 hover:bg-blue-700 px-4 py-2 rounded-lg text-sm font-medium">查看</button>
          </div>
          {showShare && (
            <div className="flex gap-2 mt-2 p-2 bg-gray-800 rounded-lg border border-gray-700">
              <input type="text" readOnly value={showShare.url} onClick={e => e.target.select()}
                className="flex-1 bg-gray-900 text-xs font-mono px-3 py-2 rounded border border-gray-700 text-blue-300" />
              <button onClick={() => { navigator.clipboard.writeText(showShare.url); setShowShare(null); }}
                className="bg-blue-600 hover:bg-blue-700 px-3 py-1 rounded text-xs">已复制</button>
              <button onClick={() => setShowShare(null)} className="text-gray-500 hover:text-white px-2">✕</button>
            </div>
          )}
          {/* 标签切换：本机 / P2P 网络 / 广播 */}
          <div className="flex gap-1 mt-3">
            <button onClick={() => setPlazaTab('local')} className={`px-4 py-1.5 text-sm rounded ${plazaTab==='local'?'bg-blue-600 text-white':'bg-gray-800 text-gray-400 hover:text-white'}`}>💻 本机 ({localColls.length})</button>
            <button onClick={() => setPlazaTab('p2p')} className={`px-4 py-1.5 text-sm rounded ${plazaTab==='p2p'?'bg-blue-600 text-white':'bg-gray-800 text-gray-400 hover:text-white'}`}>🌐 P2P 网络 ({p2pColls.length})</button>
            <button onClick={async () => {
              const h = searchInput.trim();
              if (!h) return alert('输入文件 hash 进行广播');
              try {
                await api.createAnonCollection([{path:'broadcast',hash:h}], '广播 '+h.substring(0,8));
                await api.dualAnnounce(h);
                setShowShare({url: h, name: '广播: '+h.substring(0,12)});
              } catch(e) { alert('广播失败: '+e.message); }
            }} className="px-4 py-1.5 text-sm rounded bg-amber-700 hover:bg-amber-600 text-white">📡 广播</button>
          </div>
        </div>

        {loading ? (
          <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-3 lg:grid-cols-4 gap-6">
            {Array.from({ length: 8 }).map((_, i) => <SkeletonCard key={i} />)}
          </div>
        ) : activeColls.length === 0 ? (
          <div className="text-center py-20 text-gray-500 border-2 border-dashed border-gray-700 rounded-xl">
            <p className="mb-3">{plazaTab === 'p2p' ? 'P2P 网络暂无公开合集' : '还没有创建任何合集'}</p>
            <p className="text-xs text-gray-600 mb-4">从文件管理器注册文件并自动创建匿名合集，或手动创建</p>
            <div className="flex justify-center gap-3">
              <button onClick={() => navigate('/anon/create')} className="bg-blue-600 hover:bg-blue-700 text-white px-4 py-2 rounded text-sm font-medium">
                + 创建合集
              </button>
              <button onClick={() => navigate('/files')} className="bg-gray-700 hover:bg-gray-600 text-gray-300 px-4 py-2 rounded text-sm">
                浏览文件管理器
              </button>
            </div>
          </div>
        ) : viewMode === 'list' ? (
          <div className="bg-gray-800/50 rounded-xl border border-gray-700 overflow-hidden">
            {activeColls.map(c => (
              <CollectionCard
                key={collId(c)}
                collection={c}
                viewMode="list"
                onFork={handleFork}
                onShare={handleShare}
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
                onShare={handleShare}
                onDownload={handleDownload}
              />
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
