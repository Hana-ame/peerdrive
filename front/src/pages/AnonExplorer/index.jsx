import React, { useContext, useState, useEffect } from 'react';
import * as api from '../../api';
import { useNavigate, useParams } from 'react-router-dom';
import { PageContext } from '../../App';
import CommentSection from '../../components/CommentSection';
import { extractHash } from './utils';
import SearchBar from './SearchBar';
import Toast from './Toast';
import CollectionHeader from './CollectionHeader';
import BreadcrumbNav from './BreadcrumbNav';
import FileList from './FileList';
import SingleFilePreview from './SingleFilePreview';
import EmptyState from './EmptyState';

export default function AnonExplorer() {
  const { hash: paramHash } = useParams();
  const { setPageContext } = useContext(PageContext);
  const [inputVal, setInputVal] = useState(paramHash || '');
  const [searchHash, setSearchHash] = useState(paramHash || '');
  const [collection, setCollection] = useState(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [navPath, setNavPath] = useState('');
  const [isLocal, setIsLocal] = useState(false);
  const [allCollHashes, setAllCollHashes] = useState(new Set());
  const [toastMsg, setToastMsg] = useState('');
  const [toastErr, setToastErr] = useState(false);
  const navigate = useNavigate();

  useEffect(() => { if (paramHash) { setInputVal(paramHash); setSearchHash(paramHash); } }, [paramHash]);
  useEffect(() => { if (searchHash) fetchCollection(searchHash); }, [searchHash]);
  useEffect(() => { api.listAnonCollections().then(l => { if (Array.isArray(l)) setAllCollHashes(new Set(l.map(c => c.hash))); }).catch(() => {}); }, [searchHash]);

  const showToast = (msg, isErr) => {
    setToastMsg(msg);
    setToastErr(isErr);
    const dur = isErr ? 3000 : (msg.startsWith('分享') ? 4000 : 2500);
    setTimeout(() => setToastMsg(''), dur);
  };

  const fetchCollection = async (h) => {
    if (!h) return;
    setLoading(true); setError(''); setCollection(null);
    try {
      const coll = await api.getAnonCollection(h);
      if (!coll.entries || coll.entries.length === 0) {
        await api.deleteFile(h).catch(() => {});
        setError('空合集，已自动删除');
        setLoading(false);
        return;
      }
      setCollection(coll);
    } catch { setError('合集未找到'); }
    setLoading(false); setNavPath('');
    api.listAnonCollections().then(l => setIsLocal(Array.isArray(l) && l.some(c => c.hash === h))).catch(() => {});
  };

  const handleSaveAndEdit = async () => {
    if (!collection?.entries?.length) return;
    try {
      const name = (collection.friendly_name || '合集') + ' (副本)';
      const res = await api.createAnonCollection(collection.entries, name);
      showToast('已保存到本机，可前往创建页编辑', false);
      setIsLocal(true);
      navigate(`/anon/collections/${res.hash}`);
    } catch(e) { showToast('保存失败: ' + e.message, true); }
  };

  const handleSearch = () => {
    const h = extractHash(inputVal) || inputVal.trim().toLowerCase();
    if (h.length !== 64) { showToast('请输入有效的 SHA256 Hash', true); return; }
    setSearchHash(h); setNavPath(''); navigate(`/anon/collections/${h}`);
  };

  const handleInputChange = (val) => {
    setInputVal(val);
    const h = extractHash(val);
    if (h) { setSearchHash(h); setNavPath(''); navigate(`/anon/collections/${h}`); }
  };

  const navIn = (dir) => setNavPath(prev => prev ? `${prev}/${dir}` : dir);
  const navBack = () => {
    const p = navPath.split('/');
    p.pop();
    setNavPath(p.join('/'));
  };

  const entries = collection?.entries || [];
  const fname = collection?.friendly_name || '';
  const isSingleFile = entries.length === 1 && !((entries[0]?.path) || '').includes('/');
  const totalFiles = navPath ? entries.filter(e => (e.path || '').startsWith(navPath + '/')).length : entries.length;

  return (
    <div className="flex flex-1 overflow-hidden h-full bg-gray-950">
      <div className="flex-1 flex flex-col max-w-3xl mx-auto w-full">
        <SearchBar inputVal={inputVal} loading={loading}
          onChange={handleInputChange} onSearch={handleSearch}
          onBack={() => { setCollection(null); navigate(-1); }} />

        {error && <div className="px-4 py-3"><p className="text-red-400 text-sm">{error}</p></div>}
        {loading && !collection && <div className="flex-1 flex items-center justify-center text-gray-600">加载中...</div>}

        {collection && (
          <div className="flex-1 flex flex-col overflow-hidden">
            <CollectionHeader
              navPath={navPath} fname={fname} entries={entries}
              tags={collection.tags} isSingleFile={isSingleFile}
              totalFiles={totalFiles} isLocal={isLocal}
              searchHash={searchHash}
              visibility={collection.visibility || 'public'}
              onBack={navBack} onSave={handleSaveAndEdit} onToast={showToast} />

            <Toast message={toastMsg} isError={toastErr} />

            <BreadcrumbNav navPath={navPath} fname={fname} onNavigate={setNavPath} />

            <div className="flex-1 overflow-y-auto">
              {isSingleFile && !navPath ? (
                <SingleFilePreview entry={entries[0]} searchHash={searchHash}
                  collection={collection} allCollHashes={allCollHashes} />
              ) : (
                <FileList entries={entries} navPath={navPath}
                  allCollHashes={allCollHashes} searchHash={searchHash}
                  collection={collection}
                  onNavIn={navIn}
                  onNestedCollClick={(hash) => navigate(`/anon/collections/${hash}`)} />
              )}
            </div>

            <div className="px-4 pb-4 border-t border-gray-800/50">
              <CommentSection hash={searchHash} />
            </div>
          </div>
        )}

        {!loading && !collection && !error && !paramHash && <EmptyState />}
      </div>
    </div>
  );
}
