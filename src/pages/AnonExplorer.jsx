import React, { useContext, useState, useEffect, useMemo } from 'react';
import * as api from '../api';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { PageContext } from '../App';

const SHA256_RE = /\b([a-f0-9]{64})\b/i;

function extractHash(text) {
  const m = (text || '').match(SHA256_RE);
  return m ? m[1].toLowerCase() : null;
}

function buildTree(entries) {
  if (!entries || entries.length === 0) return [];
  const node = {};
  for (const e of entries) {
    const parts = e.path.split('/');
    let cur = node;
    for (let i = 0; i < parts.length; i++) {
      const seg = parts[i];
      const isLast = i === parts.length - 1;
      if (!cur[seg]) {
        cur[seg] = { _children: {}, _files: [] };
      }
      if (isLast) {
        cur[seg]._files.push({ name: seg, hash: e.hash, path: e.path });
      }
      cur = cur[seg]._children;
    }
  }
  function toArray(obj, prefix = '') {
    const result = [];
    for (const key of Object.keys(obj).sort()) {
      const item = obj[key];
      const fullPath = prefix ? `${prefix}/${key}` : key;
      if (item._files.length === 1 && item._files[0].name === key && Object.keys(item._children).length === 0) {
        result.push({ name: key, path: fullPath, isDir: false, children: [], files: item._files });
      } else {
        result.push({
          name: key,
          path: fullPath,
          isDir: true,
          children: toArray(item._children, fullPath),
          files: item._files,
        });
      }
    }
    return result;
  }
  // Flatten tree into rows for rendering
  function flatten(nodes, depth, expandedDirs) {
    const rows = [];
    for (const node of nodes) {
      const exp = expandedDirs.has(node.path);
      rows.push({ ...node, depth, rowType: 'dir' });
      if (exp) {
        for (const child of node.children) {
          rows.push(...flatten([child], depth + 1, expandedDirs));
        }
        for (const f of node.files) {
          rows.push({ ...f, depth: depth + 1, rowType: 'file' });
        }
      }
    }
    return rows;
  }
  const roots = [];
  const topFiles = [];
  for (const key of Object.keys(node).sort()) {
    const item = node[key];
    if (Object.keys(item._children).length > 0) {
      roots.push({
        name: key,
        path: key,
        isDir: true,
        children: toArray(item._children, key),
        files: item._files,
      });
    } else {
      for (const f of item._files) {
        topFiles.push({ ...f, depth: 0, rowType: 'file' });
      }
    }
  }
  return { roots, topFiles };
}

function TreeRow({ row, searchHash, expandedDirs, setExpandedDirs }) {
  if (row.rowType === 'file') {
    const url = api.getAnonFileDownloadUrl(searchHash, row.path);
    return (
      <tr className="hover:bg-gray-800/50">
        <td className="py-2 font-mono text-sm text-blue-300" style={{ paddingLeft: `${row.depth * 20 + 8}px` }}>
          <a href={url} target="_blank" rel="noreferrer" className="hover:underline">{row.name}</a>
        </td>
        <td className="py-2 font-mono text-xs text-gray-500 truncate">{(row.hash || '').substring(0, 24)}...</td>
        <td className="py-2 text-right">
          <a href={url} target="_blank" rel="noreferrer" className="text-blue-400 hover:underline text-xs">下载</a>
        </td>
      </tr>
    );
  }
  const isExp = expandedDirs.has(row.path);
  const toggle = () => {
    setExpandedDirs(prev => {
      const next = new Set(prev);
      if (isExp) next.delete(row.path);
      else next.add(row.path);
      return next;
    });
  };
  return (
    <tr className="hover:bg-gray-800/50 cursor-pointer" onClick={toggle}>
      <td className="py-2 text-sm" style={{ paddingLeft: `${row.depth * 20 + 8}px` }}>
        <span className="mr-1.5">{isExp ? '📂' : '📁'}</span>
        <span className="text-gray-200 font-mono">{row.name}/</span>
      </td>
      <td className="py-2 text-xs text-gray-500"></td>
      <td className="py-2 text-right text-xs text-gray-500">{row.files.length + (row.children ? row.children.length : 0)} 项</td>
    </tr>
  );
}

export default function AnonExplorer() {
  const { hash: paramHash } = useParams();
  const { setPageContext } = useContext(PageContext);
  const [inputVal, setInputVal] = useState(paramHash || '');
  const [searchHash, setSearchHash] = useState(paramHash || '');
  const [collection, setCollection] = useState(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [expandedDirs, setExpandedDirs] = useState(new Set());
  const navigate = useNavigate();

  useEffect(() => {
    if (paramHash) {
      setInputVal(paramHash);
      setSearchHash(paramHash);
    }
  }, [paramHash]);

  useEffect(() => {
    if (searchHash) fetchCollection(searchHash);
  }, [searchHash]);

  useEffect(() => {
    if (collection) {
      const name = collection.friendly_name || searchHash.substring(0, 12);
      document.title = `${name} — 匿名合集`;
    } else if (searchHash) {
      document.title = `${searchHash.substring(0, 12)}... — 加载中`;
    } else {
      document.title = '匿名合集浏览器';
    }
    return () => { document.title = 'Peerdrive'; };
  }, [collection, searchHash]);

  useEffect(() => {
    if (collection) {
      setPageContext({ type: 'anonExplorer', version: collection.version, entryCount: collection.entries?.length || 0, friendlyName: collection.friendly_name || '', hash: searchHash.substring(0, 16) });
    } else {
      setPageContext({ type: 'anonExplorer', hash: searchHash ? searchHash.substring(0, 16) : '' });
    }
  }, [collection, searchHash]);

  const fetchCollection = async (h) => {
    if (!h) return;
    setLoading(true);
    setError('');
    setCollection(null);
    try {
      const coll = await api.getAnonCollection(h);
      setCollection(coll);
    } catch (err) {
      setError('合集未找到');
    } finally {
      setLoading(false);
    }
  };

  const handleFork = () => {
    navigate('/anon/create', { state: { forkFrom: collection, sourceHash: searchHash } });
  };

  const handleCommit = () => {
    navigate('/anon/create', { state: { editFrom: collection, savedHash: searchHash } });
  };

  const handleInputChange = (val) => {
    setInputVal(val);
    const extracted = extractHash(val);
    if (extracted) {
      setSearchHash(extracted);
      navigate(`/anon/collections/${extracted}`);
    }
  };

  const handleSearch = () => {
    const extracted = extractHash(inputVal);
    const h = extracted || inputVal.trim().toLowerCase();
    if (h.length !== 64) return alert('请输入有效的 SHA256 Hash (64位十六进制)');
    setSearchHash(h);
    navigate(`/anon/collections/${h}`);
  };

  const tree = useMemo(() => {
    if (!collection?.entries) return { roots: [], topFiles: [] };
    const entries = collection.entries || [];
    const sorted = [...entries].sort((a, b) => a.path.localeCompare(b.path));
    return buildTree(sorted);
  }, [collection?.entries]);

  const hasDirs = tree.roots.length > 0;
  const createdAt = collection?.created_at ? new Date(collection.created_at).toLocaleString() : '';
  const fname = collection?.friendly_name || '';

  return (
    <div className="flex flex-1 overflow-hidden h-full bg-gray-950">
      <div className="flex-1 flex flex-col max-w-4xl mx-auto w-full">
        <div className="h-14 flex items-center px-4 shrink-0 space-x-3 border-b border-gray-800">
          <Link to="/anon/create" className="text-sm bg-blue-600 hover:bg-blue-700 px-3 py-1 rounded">创建合集</Link>
          <h2 className="text-base font-bold">匿名合集</h2>
          {fname && <span className="text-gray-400 text-sm">{fname}</span>}
        </div>

        <div className="flex items-center gap-2 px-4 py-3 shrink-0">
          <div className="relative flex-1">
            <input
              type="text"
              value={inputVal}
              onChange={(e) => handleInputChange(e.target.value)}
              onPaste={(e) => {
                const pasted = e.clipboardData.getData('text');
                const extracted = extractHash(pasted);
                if (extracted) {
                  e.preventDefault();
                  setInputVal(extracted);
                  setSearchHash(extracted);
                  navigate(`/anon/collections/${extracted}`);
                }
              }}
              onKeyDown={(e) => { if (e.key === 'Enter') handleSearch(); }}
              placeholder="输入 SHA256 或搜索合集名称"
              className="w-full bg-gray-800 border border-gray-600 px-3 py-2 rounded text-sm font-mono focus:outline-none focus:border-blue-500"
            />
          </div>
          <button onClick={handleSearch}
            disabled={loading}
            className="bg-blue-600 hover:bg-blue-700 px-4 py-2 rounded text-sm font-medium disabled:opacity-50">
            {loading ? '加载中...' : '查看'}
          </button>
        </div>

        {error && (
          <div className="px-4 py-3"><p className="text-red-400 text-sm">{error}</p></div>
        )}
        {loading && !collection && (
          <div className="flex-1 flex items-center justify-center text-gray-600">加载中...</div>
        )}

        {collection && (
          <div className="flex-1 overflow-y-auto px-4">
            <div className="bg-gray-800 p-4 rounded-lg mb-4 border border-gray-700 mt-2">
              <div className="flex justify-between items-center">
                <div>
                  <h3 className="text-lg font-bold text-gray-100">{fname || '未命名合集'}</h3>
                  {createdAt && <p className="text-gray-500 text-xs mt-1">创建于 {createdAt}</p>}
                </div>
                <div className="flex items-center gap-2">
                  <span className="text-xs text-gray-600">{collection.entries?.length || 0} 个文件</span>
                  <button onClick={handleCommit} className="bg-green-600 hover:bg-green-700 px-3 py-1 rounded text-xs">Commit</button>
                  <button onClick={handleFork} className="bg-purple-600 hover:bg-purple-700 px-3 py-1 rounded text-xs">克隆并修改</button>
                </div>
              </div>
              <details className="mt-3">
                <summary className="text-xs text-gray-500 cursor-pointer hover:text-gray-400">技术详情</summary>
                <div className="mt-2 space-y-1 text-xs text-gray-500 font-mono">
                  <div>版本: v{collection.version} · SHA256: {searchHash.substring(0, 32)}...</div>
                </div>
              </details>
            </div>

            <table className="w-full text-left border-collapse mb-6">
              <thead>
                <tr className="border-b border-gray-700 text-gray-400 text-xs">
                  <th className="pb-2 font-medium">{hasDirs ? '目录/文件' : '文件'}</th>
                  <th className="pb-2 font-medium w-48">Hash</th>
                  <th className="pb-2 font-medium w-24 text-right">操作</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-800">
                {hasDirs && tree.roots.map((n, i) => (
                  <TreeRow key={n.path} row={n} searchHash={searchHash} expandedDirs={expandedDirs} setExpandedDirs={setExpandedDirs} />
                ))}
                {tree.topFiles.map((f, i) => (
                  <TreeRow key={f.path + i} row={f} searchHash={searchHash} expandedDirs={expandedDirs} setExpandedDirs={setExpandedDirs} />
                ))}
              </tbody>
            </table>
          </div>
        )}

        {!loading && !collection && !error && !paramHash && (
          <div className="flex-1 flex flex-col items-center justify-center text-gray-600 text-sm gap-4">
            <p>输入合集 Hash 查看内容</p>
            <div className="flex gap-3">
              <Link to="/anon/create" className="text-blue-400 hover:underline text-xs">创建合集</Link>
              <Link to="/" className="text-blue-400 hover:underline text-xs">浏览公开合集</Link>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
