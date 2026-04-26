import React, { useContext, useState, useEffect, useCallback } from 'react';
import * as api from '../api';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { PageContext } from '../App';

const SHA256_RE = /\b([a-f0-9]{64})\b/i;

function extractHash(text) {
  const m = (text || '').match(SHA256_RE);
  return m ? m[1].toLowerCase() : null;
}

export default function AnonExplorer() {
  const { hash: paramHash } = useParams();
  const { setPageContext } = useContext(PageContext);
  const [inputVal, setInputVal] = useState(paramHash || '');
  const [searchHash, setSearchHash] = useState(paramHash || '');
  const [collection, setCollection] = useState(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const navigate = useNavigate();



  // sync URL param <-> input
  useEffect(() => {
    if (paramHash) {
      setInputVal(paramHash);
      setSearchHash(paramHash);
    }
  }, [paramHash]);

  // load collection when hash is available
  useEffect(() => {
    if (searchHash) {
      fetchCollection(searchHash);
    }
  }, [searchHash]);

  // document.title
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

  return (
    <div className="flex flex-1 overflow-hidden h-full bg-gray-950">
      <div className="flex-1 flex flex-col max-w-4xl mx-auto w-full">
        <div className="h-14 flex items-center px-4 shrink-0 space-x-3 border-b border-gray-800">
          <Link to="/anon/create" className="text-sm bg-blue-600 hover:bg-blue-700 px-3 py-1 rounded">+ 创建合集</Link>
          <h2 className="text-base font-bold">匿名合集</h2>
          {collection?.friendly_name && (
            <span className="text-gray-400 text-sm">{collection.friendly_name}</span>
          )}
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
              placeholder="输入 SHA256 Hash (或粘贴含 hash 的链接，自动提取)"
              className="w-full bg-gray-800 border border-gray-600 px-3 py-2 rounded text-sm font-mono focus:outline-none focus:border-blue-500"
            />
            {inputVal && extractHash(inputVal) && (
              <span className="absolute right-2 top-1/2 -translate-y-1/2 text-[10px] text-green-500 font-mono">
                ✓ {extractHash(inputVal).substring(0, 12)}...
              </span>
            )}
          </div>
          <button onClick={handleSearch}
            disabled={loading}
            className="bg-blue-600 hover:bg-blue-700 px-4 py-2 rounded text-sm font-medium disabled:opacity-50">
            {loading ? '加载中...' : '查看'}
          </button>
        </div>

        {error && (
          <div className="px-4 py-3">
            <p className="text-red-400 text-sm">{error}</p>
          </div>
        )}

        {loading && !collection && (
          <div className="flex-1 flex items-center justify-center text-gray-600">加载中...</div>
        )}

        {collection && (
          <div className="flex-1 overflow-y-auto px-4">
            <div className="bg-gray-800 p-4 rounded-lg mb-4 border border-gray-700 mt-2">
              <div className="flex justify-between items-center mb-2">
                <div>
                  <span className="text-gray-400 text-sm">Version: </span>
                  <span className="font-bold text-blue-400">{collection.version}</span>
                </div>
                <div className="text-gray-500 text-xs">
                  {collection.created_at ? new Date(collection.created_at).toLocaleString() : ''}
                </div>
              </div>
              {collection.friendly_name && (
                <p className="text-gray-300 text-sm">{collection.friendly_name}</p>
              )}
              <div className="mt-3 flex gap-2 flex-wrap">
                <code className="text-xs bg-gray-900 px-2 py-1 rounded text-gray-400 truncate max-w-[300px]">
                  {searchHash}
                </code>
                <button onClick={handleCommit}
                   className="bg-green-600 hover:bg-green-700 px-3 py-1 rounded text-xs">
                  Commit
                </button>
                <button onClick={handleFork}
                   className="bg-purple-600 hover:bg-purple-700 px-3 py-1 rounded text-xs">
                  克隆并修改
                </button>
              </div>
            </div>

            <table className="w-full text-left border-collapse mb-6">
              <thead>
                <tr className="border-b border-gray-700 text-gray-400 text-xs">
                  <th className="pb-2 font-medium">路径</th>
                  <th className="pb-2 font-medium w-48">文件 Hash</th>
                  <th className="pb-2 font-medium w-24 text-right">操作</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-800">
                {collection.entries?.map((entry, idx) => (
                  <tr key={idx} className="hover:bg-gray-800/50">
                    <td className="py-2 font-mono text-sm text-blue-300">{entry.path}</td>
                    <td className="py-2 font-mono text-xs text-gray-500 truncate">{(entry.hash || '').substring(0, 24)}...</td>
                    <td className="py-2 text-right">
                      <a href={api.getAnonFileDownloadUrl(searchHash, entry.path)}
                        target="_blank" rel="noreferrer"
                        className="text-blue-400 hover:underline text-xs">下载</a>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}

        {!loading && !collection && !error && !paramHash && (
          <div className="flex-1 flex items-center justify-center text-gray-600 text-sm">
            输入合集 Hash 查看内容，或粘贴含 hash 的链接自动提取
          </div>
        )}
      </div>
    </div>
  );
}
