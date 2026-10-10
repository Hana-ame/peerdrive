// CollectionView.jsx — collection display page: input a collection (sha or parsed
// JSON) and browse it as a folder tree (see CollectionBrowser).
//
// 加载路径：
//   1. 按 collection sha：collection 本身就是保存在 sha-文件系统里的 JSON（内容寻址），
//      所以首选 ws.download(sha)（req 帧，与文件取数同一通道）拿字节 → JSON.parse；
//      拿不到（非 JSON / 节点上没有）时兜底走本地管理面 GET /anon/collections/{hash}
//      （当前后端返回的是 providers 旧格式，归一化层同样能读）。
//   2. 粘贴已解析/手写的 collection JSON：直接进入浏览，不经过网络。
// 路由：/collection/:hash 直接按 sha 加载；/collection 无参时给输入面板。
import React, { useCallback, useEffect, useState } from 'react';
import { useParams } from 'react-router-dom';
import * as ws from '../../../platform/transport-ws';
import CollectionBrowser from '../components/CollectionBrowser';

import { manifestCache } from '../../../lib/cache';

import { getNodeSession } from '../../../lib/nodeSession';

// parseManifest：校验并规整 collection 对象。为什么这里强制 entries 存在：
// 文件夹视图的输入契约是"有 entries 数组的集合 JSON"，缺了它没有可展示的结构，
// 早失败比渲染出一个空文件夹更诚实。
function parseManifest(data) {
  if (!data || typeof data !== 'object') throw new Error('collection JSON is not an object');
  if (!Array.isArray(data.entries)) throw new Error('collection JSON is missing "entries" array');
  return data;
}

// readStreamAsText：从 ReadableStream 流式读取并用 TextDecoder('utf-8', { stream: true }) 增量解码。
// 避免 ws.download 的全量 Uint8Array 内存堆积，降低大 manifest 峰值内存。
async function readStreamAsText(readableStream) {
  const reader = readableStream.getReader();
  const decoder = new TextDecoder('utf-8');
  let result = '';
  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) {
        result += decoder.decode();
        break;
      }
      if (value) {
        result += decoder.decode(value, { stream: true });
      }
    }
  } finally {
    reader.releaseLock();
  }
  return result;
}

export default function CollectionView() {
  const { hash } = useParams(); // 路由 /collection/:hash
  const [input, setInput] = useState(hash || ''); // 输入框里的 sha
  const [json, setJson] = useState(''); // 粘贴 JSON 文本
  const [lastErr, setLastErr] = useState('');
  const [state, setState] = useState({ phase: hash ? 'loading' : 'input', collection: null, source: '' });

  const loadBySha = useCallback(async (sha) => {
    setLastErr('');
    // 缓存优先：内容寻址保证 sha 对应的 manifest 永不改变
    const cached = manifestCache.get(sha);
    if (cached) {
      setState({ phase: 'ready', collection: cached, source: sha });
      return;
    }

    setState({ phase: 'loading', collection: null, source: sha });
    try {
      let data;
      const session = getNodeSession();
      // 如果明确是无 WS 连接但有 PeerJS 客户端，优先从 PeerJS 共享范围读取
      if (session?.client && typeof ws.getStatus === 'function' && ws.getStatus() === 'closed') {
        try {
          const snap = await session.client.shares();
          const match = (snap?.collections || []).find(c => c.hash === sha || c.name === sha);
          if (match && Array.isArray(match.entries)) {
            data = parseManifest({
              hash: match.hash,
              friendly_name: match.name,
              entries: match.entries,
              tags: match.tags,
              created_at: '',
            });
          }
        } catch { /* ignore */ }
      }

      // 首选：内容寻址直取（使用 ws.downloadStream 流式读取）
      if (!data) {
        try {
          const stream = ws.downloadStream(sha);
          const text = await readStreamAsText(stream);
          data = parseManifest(JSON.parse(text));
        } catch {
          try {
            data = parseManifest(await ws.admin('GET', `/anon/collections/${sha}`));
          } catch (adminErr) {
            // 兜底：若 WS 取数失败且存在 PeerJS 会话，尝试 PeerJS shares 或 stream
            if (session?.client) {
              try {
                const snap = await session.client.shares();
                const match = (snap?.collections || []).find(c => c.hash === sha || c.name === sha);
                if (match && Array.isArray(match.entries)) {
                  data = parseManifest({
                    hash: match.hash,
                    friendly_name: match.name,
                    entries: match.entries,
                    tags: match.tags,
                    created_at: '',
                  });
                }
              } catch { /* ignore */ }
              if (!data) {
                const chunks = [];
                for await (const chunk of session.client.stream(sha)) {
                  chunks.push(chunk);
                }
                const blob = new Blob(chunks);
                const text = await blob.text();
                data = parseManifest(JSON.parse(text));
              }
            } else {
              throw adminErr;
            }
          }
        }
      }

      if (!data) {
        throw new Error('Failed to load collection manifest');
      }

      manifestCache.set(sha, data);
      setState({ phase: 'ready', collection: data, source: sha });
    } catch (e) {
      setLastErr(e?.message || String(e));
      setState({ phase: 'error', collection: null, source: sha });
    }
  }, []);

  // hash 参数变化（路由跳转）→ 重新加载
  useEffect(() => { if (hash) loadBySha(hash); }, [hash, loadBySha]);

  const loadJson = () => {
    try {
      const data = parseManifest(JSON.parse(json));
      setLastErr('');
      setState({ phase: 'ready', collection: data, source: 'pasted JSON' });
    } catch (e) {
      setLastErr(e?.message || String(e));
    }
  };

  const inputPanel = (
    <div className="card-surface p-5 space-y-4">
      <div>
        <label className="block text-xs text-gray-400 mb-1.5">Collection SHA</label>
        <div className="flex gap-2">
          <input
            value={input}
            onChange={(e) => setInput(e.target.value)}
            placeholder="64-hex sha of the collection JSON"
            className="input-base flex-1 font-mono text-xs"
          />
          <button onClick={() => input.trim() && loadBySha(input.trim())} className="btn-brand shrink-0">Load</button>
        </div>
        <p className="text-[11px] text-gray-600 mt-1.5">
          The collection itself is a JSON stored in the sha filesystem — enter its sha to fetch it by content address.
        </p>
      </div>
      <div className="border-t border-white/[0.06]" />
      <div>
        <label className="block text-xs text-gray-400 mb-1.5">…or paste collection JSON</label>
        <textarea
          value={json}
          onChange={(e) => setJson(e.target.value)}
          rows={6}
          placeholder={'{\n  "friendly_name": "demo",\n  "entries": [\n    { "path": "photos/a.jpg", "sha": "<64hex>", "preview": "<64hex>" }\n  ]\n}'}
          className="input-base w-full font-mono text-xs"
        />
        <button onClick={loadJson} className="btn-brand mt-2">Browse JSON</button>
      </div>
    </div>
  );

  return (
    <div className="p-8 overflow-y-auto h-full">
      <div className="max-w-5xl mx-auto">
        <div className="flex items-center justify-between mb-5">
          <div>
            <h1 className="text-2xl font-bold">Collection</h1>
            <p className="text-sm text-gray-500 mt-0.5">Browse a collection manifest as a folder tree</p>
          </div>
          {state.phase !== 'input' && (
            <button onClick={() => setState({ phase: 'input', collection: null, source: '' })} className="btn-ghost">Load another</button>
          )}
        </div>

        {lastErr && (
          <div className="mb-4 text-xs text-red-400 bg-red-400/10 border border-red-400/20 rounded-lg px-3 py-2">{lastErr}</div>
        )}

        {state.phase === 'input' && inputPanel}

        {state.phase === 'loading' && (
          <div className="text-center py-16 text-gray-500 text-sm">Loading collection {state.source.slice(0, 16)}…</div>
        )}

        {state.phase === 'error' && (
          <div className="text-center py-16 text-gray-500 text-sm">
            <p>Could not load collection {state.source.slice(0, 16)}…</p>
            <button onClick={() => setState({ phase: 'input', collection: null, source: '' })} className="btn-ghost mt-4">Back to input</button>
          </div>
        )}

        {state.phase === 'ready' && state.collection && (
          <div>
            <div className="text-xs text-gray-500 mb-3 flex items-center gap-1 flex-wrap">
              {typeof state.collection.friendly_name === 'string' && state.collection.friendly_name && (
                <span>{state.collection.friendly_name}</span>
              )}
              {state.source && state.source !== 'pasted JSON' && (
                <span className="font-mono">{state.source.slice(0, 16)}…</span>
              )}
              <span>{state.collection.entries.length} entries</span>
            </div>
            <CollectionBrowser collection={state.collection} onError={(m) => setLastErr(m)} />
          </div>
        )}
      </div>
    </div>
  );
}
