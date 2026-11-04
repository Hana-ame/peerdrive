import { useState, useEffect, useCallback, useMemo } from 'react';
import * as ws from '../../../platform/transport-ws';
import { entrySha } from '../../../platform/shared/collectionTree';
import { getNodeSession, onNodeSession } from '../../../lib/nodeSession';

export function useCollections() {
  const [tab, setTab] = useState('list'); // list | create
  const [collections, setCollections] = useState(null);
  const [err, setErr] = useState('');
  const [busy, setBusy] = useState(false);
  const [wsStatus, setWsStatus] = useState(() => ws.getStatus());
  const [peerSession, setPeerSession] = useState(() => getNodeSession());

  useEffect(() => {
    const offWs = ws.onStatus((s) => setWsStatus(s));
    const offSession = onNodeSession((s) => setPeerSession(s));
    return () => {
      offWs();
      offSession();
    };
  }, []);

  const activeSession = peerSession?.client ? peerSession : getNodeSession();
  const hasPeerClient = Boolean(activeSession?.client);
  const isWsOpen = wsStatus === 'open';
  const isGuest = hasPeerClient || (!isWsOpen && Boolean(activeSession?.peerId));
  const canCreate = isWsOpen && !hasPeerClient;

  // Enforce permission separation: if visitor mode becomes active, force list mode
  useEffect(() => {
    if ((isGuest || !canCreate) && tab === 'create') {
      setTab('list');
    }
  }, [isGuest, canCreate, tab]);

  // Create-mode state
  const [files, setFiles] = useState([]);
  const [selected, setSelected] = useState([]);
  const [name, setName] = useState('');
  const [tagsInput, setTagsInput] = useState('');
  const [visibility, setVisibility] = useState('public');
  const [accessPolicy, setAccessPolicy] = useState('public');
  const [passcode, setPasscode] = useState('');

  // Search & filter state
  const [searchQuery, setSearchQuery] = useState('');
  const [selectedTag, setSelectedTag] = useState('');
  const [viewMode, setViewMode] = useState('list'); // list | grid

  // Detail-mode state
  const [detail, setDetail] = useState(null); // {hash, data}
  const [unlockPasscode, setUnlockPasscode] = useState('');

  const loadList = useCallback(async () => {
    setErr('');
    const client = peerSession?.client || getNodeSession()?.client;
    if (client) {
      try {
        const snap = await client.shares();
        const colls = (snap?.collections || []).map((c, i) => ({
          hash: c.hash || `c-${i}`,
          current_hash: c.hash || `c-${i}`,
          friendly_name: c.name || c.hash || 'Unnamed Collection',
          entry_count: c.size ?? (Array.isArray(c.entries) ? c.entries.length : 0),
          entries: (c.entries || []).map((e) => ({
            path: e.path || e.name || 'unnamed',
            hash: e.hash || '',
            mime_type: e.mime || '',
            providers: [{ type: 'sha256', value: e.hash || '', mime_type: e.mime || '' }],
          })),
          tags: c.tags || [],
          visibility: c.access_policy || 'public',
          version: 1,
          is_protected: Boolean(c.is_protected),
          created_at: '',
          isRemotePeer: true,
        }));
        setCollections(colls);
        return;
      } catch (loadErr) {
        setErr('无法获取远程节点共享合集: ' + (loadErr?.message || String(loadErr)));
        setCollections([]);
        return;
      }
    }

    try {
      const res = await ws.admin('GET', '/anon/collections');
      setCollections(Array.isArray(res) ? res : []);
    } catch {
      setErr('');
      setCollections([]);
    }
  }, [peerSession]);

  useEffect(() => {
    loadList();
  }, [loadList]);

  const openCreate = async () => {
    if (!canCreate) {
      setErr(
        isGuest
          ? 'Visitors have read-only access and cannot create collections'
          : 'Node connection required to create collections'
      );
      return;
    }
    setErr('');
    setTab('create');
    setSelected([]);
    setName('');
    setTagsInput('');
    setVisibility('public');
    setAccessPolicy('public');
    setPasscode('');
    try {
      const res = await ws.admin('GET', '/files');
      setFiles(Array.isArray(res) ? res : []);
    } catch (e) {
      setErr(e?.message || String(e));
      setFiles([]);
    }
  };

  const toggleFile = (hash) => {
    setSelected((prev) => (prev.includes(hash) ? prev.filter((h) => h !== hash) : [...prev, hash]));
  };

  const create = async () => {
    if (!canCreate) {
      setErr('Permission denied: visitors cannot create collections');
      return;
    }
    if (!name.trim()) {
      setErr('Please enter a collection name');
      return;
    }
    const chosen = files.filter((f) => selected.includes(f.hash));
    if (chosen.length === 0) {
      setErr('Please select at least one file');
      return;
    }
    if (accessPolicy === 'protected' && !passcode.trim()) {
      setErr('Please enter a passcode for protected access policy');
      return;
    }
    const entries = chosen.map((f) => ({
      path: f.filename,
      providers: [{ type: 'sha256', value: f.hash, mime_type: f.mime_type }],
    }));
    const tags = tagsInput
      .split(',')
      .map((t) => t.trim())
      .filter(Boolean);

    setBusy(true);
    try {
      await ws.admin('POST', '/anon/collections', {
        friendly_name: name.trim(),
        entries,
        tags,
        visibility,
        access_policy: accessPolicy,
        passcode: accessPolicy === 'protected' ? passcode.trim() : '',
      });
      setTab('list');
      await loadList();
    } catch (e) {
      setErr(e?.message || String(e));
    } finally {
      setBusy(false);
    }
  };

  const openDetail = async (c) => {
    const targetHash = c.hash || c.current_hash;
    if (detail?.hash === targetHash) {
      setDetail(null);
      return;
    }
    setErr('');
    setUnlockPasscode('');
    setDetail({ hash: targetHash, data: null });

    if (Array.isArray(c.entries) && c.entries.length > 0) {
      setDetail({ hash: targetHash, data: c });
      return;
    }

    const client = peerSession?.client || getNodeSession()?.client;
    if (client) {
      try {
        const snap = await client.shares();
        const match = (snap?.collections || []).find(
          (col) => col.hash === targetHash || col.name === targetHash
        );
        if (match) {
          setDetail({
            hash: targetHash,
            data: {
              hash: match.hash,
              friendly_name: match.name,
              entries: (match.entries || []).map((entry) => ({
                path: entry.path || entry.name || 'unnamed',
                hash: entry.hash || '',
                mime_type: entry.mime || '',
                providers: [{ type: 'sha256', value: entry.hash || '', mime_type: entry.mime || '' }],
              })),
              tags: match.tags || [],
              is_protected: Boolean(match.is_protected),
              visibility: match.access_policy || 'public',
            },
          });
          return;
        }
      } catch (e) {
        setErr(e?.message || String(e));
        setDetail(null);
        return;
      }
    }

    try {
      const d = await ws.admin('GET', `/anon/collections/${targetHash}`);
      setDetail({ hash: targetHash, data: d });
    } catch (e) {
      setErr(e?.message || String(e));
      setDetail(null);
    }
  };

  const unlockProtected = async (hash) => {
    if (!unlockPasscode.trim()) return;
    if (peerSession?.client || getNodeSession()?.client) return;
    setBusy(true);
    try {
      const d = await ws.admin(
        'GET',
        `/anon/collections/${hash}?passcode=${encodeURIComponent(unlockPasscode.trim())}`
      );
      setDetail({ hash, data: d });
    } catch (e) {
      setErr(e?.message || String(e));
    } finally {
      setBusy(false);
    }
  };

  const downloadEntry = async (hash, path) => {
    try {
      const fname = (path || 'download').split('/').pop();
      if (peerSession?.client) {
        await peerSession.client.saveAs(hash, fname);
        return;
      }
      const query = unlockPasscode ? `?passcode=${encodeURIComponent(unlockPasscode.trim())}` : '';
      await ws.downloadToFile(hash, path || 'download', query);
    } catch (e) {
      setErr(e?.message || String(e));
    }
  };

  const entryHash = (e) => entrySha(e);

  // Compute all available tags across collections with occurrence count
  const allTagsWithCounts = useMemo(() => {
    if (!collections) return [];
    const map = {};
    collections.forEach((c) => {
      if (Array.isArray(c.tags)) {
        c.tags.forEach((t) => {
          if (t) map[t] = (map[t] || 0) + 1;
        });
      }
    });
    return Object.entries(map).map(([tag, count]) => ({ tag, count }));
  }, [collections]);

  // Filter collections by search query and selected tag
  const filteredCollections = useMemo(() => {
    if (!collections) return [];
    return collections.filter((c) => {
      const matchQuery =
        !searchQuery.trim() ||
        (c.friendly_name && c.friendly_name.toLowerCase().includes(searchQuery.toLowerCase())) ||
        (c.hash && c.hash.toLowerCase().includes(searchQuery.toLowerCase())) ||
        (Array.isArray(c.tags) && c.tags.some((t) => t.toLowerCase().includes(searchQuery.toLowerCase())));

      const matchTag = !selectedTag || (Array.isArray(c.tags) && c.tags.includes(selectedTag));

      return matchQuery && matchTag;
    });
  }, [collections, searchQuery, selectedTag]);

  return {
    tab,
    setTab,
    collections,
    filteredCollections,
    err,
    setErr,
    busy,
    isGuest,
    canCreate,
    viewMode,
    setViewMode,
    loadList,
    // Search & tags
    searchQuery,
    setSearchQuery,
    selectedTag,
    setSelectedTag,
    allTagsWithCounts,
    // Create state & handlers
    openCreate,
    create,
    name,
    setName,
    tagsInput,
    setTagsInput,
    visibility,
    setVisibility,
    accessPolicy,
    setAccessPolicy,
    passcode,
    setPasscode,
    files,
    selected,
    toggleFile,
    // Detail state & handlers
    detail,
    openDetail,
    unlockPasscode,
    setUnlockPasscode,
    unlockProtected,
    downloadEntry,
    entryHash,
  };
}
