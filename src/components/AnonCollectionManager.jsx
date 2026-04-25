import React, { useState, useEffect } from 'react';
import * as api from '../api';

export default function AnonCollectionManager({ initialHash }) {
  const [viewHash, setViewHash] = useState(initialHash || '');
  const [collection, setCollection] = useState(null);
  
  const [newEntries, setNewEntries] = useState([{ path: '', hash: '' }]);
  
  const [forkAddPath, setForkAddPath] = useState('');
  const [forkAddHash, setForkAddHash] = useState('');
  const [forkRemovePath, setForkRemovePath] = useState('');

  useEffect(() => {
    if (initialHash) fetchCollection(initialHash);
  }, [initialHash]);

  const fetchCollection = async (hash) => {
    if (!hash) return;
    try {
      const res = await api.getAnonCollection(hash);
      setCollection(res);
      setViewHash(hash);
    } catch {
      alert('合集不存在');
      setCollection(null);
    }
  };

  const handleCreate = async (e) => {
    e.preventDefault();
    const filtered = newEntries.filter(e => e.path && e.hash);
    if (filtered.length === 0) return;
    try {
      const res = await api.createAnonCollection(filtered);
      fetchCollection(res.hash);
      setNewEntries([{ path: '', hash: '' }]);
    } catch (err) {
      alert('创建失败: ' + err.message);
    }
  };

  const handleFork = async (e) => {
    e.preventDefault();
    const add_entries = forkAddPath && forkAddHash ? [{ path: forkAddPath, hash: forkAddHash }] : [];
    const remove_paths = forkRemovePath ? [forkRemovePath] : [];
    try {
      const res = await api.forkAnonCollection(viewHash, add_entries, remove_paths);
      fetchCollection(res.hash);
    } catch (err) {
      alert('Fork 失败: ' + err.message);
    }
  };

  return (
    <div>
      <h2>匿名合集 (不可变)</h2>

      <div style={{marginBottom: '30px', padding: '15px', border: '1px solid #000'}}>
        <form onSubmit={(e) => { e.preventDefault(); fetchCollection(viewHash); }}>
          <input value={viewHash} onChange={e => setViewHash(e.target.value)} placeholder="输入 Hash 查看合集" style={{width: '500px'}} />
          <button type="submit">查看</button>
        </form>

        {collection && (
          <div style={{marginTop: '15px'}}>
            <p>Version: {collection.version} | Created: {collection.created_at}</p>
            <table style={{width: '100%', textAlign: 'left', borderCollapse: 'collapse'}}>
              <thead>
                <tr>
                  <th style={{borderBottom:'1px solid #000'}}>Path</th>
                  <th style={{borderBottom:'1px solid #000'}}>Hash</th>
                  <th style={{borderBottom:'1px solid #000'}}>Action</th>
                </tr>
              </thead>
              <tbody>
                {collection.entries.map((entry, idx) => (
                  <tr key={idx}>
                    <td style={{borderBottom:'1px solid #ccc'}}>{entry.path}</td>
                    <td style={{borderBottom:'1px solid #ccc', fontSize:'12px'}}>{entry.hash}</td>
                    <td style={{borderBottom:'1px solid #ccc'}}>
                      <a href={api.getAnonFileDownloadUrl(viewHash, entry.path)} target="_blank" rel="noreferrer">下载</a>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>

            <details style={{marginTop: '15px'}}>
              <summary><b>Fork 此合集</b></summary>
              <form onSubmit={handleFork} style={{marginTop: '10px', background:'#eee', padding:'10px'}}>
                <label>新增文件:</label><br/>
                <input value={forkAddPath} onChange={e => setForkAddPath(e.target.value)} placeholder="路径" style={{width:'40%'}} />
                <input value={forkAddHash} onChange={e => setForkAddHash(e.target.value)} placeholder="Hash" style={{width:'50%'}} /><br/>
                
                <label>移除文件:</label><br/>
                <input value={forkRemovePath} onChange={e => setForkRemovePath(e.target.value)} placeholder="路径" style={{width:'90%'}} /><br/>
                
                <button type="submit" style={{marginTop:'10px'}}>执行 Fork</button>
              </form>
            </details>
          </div>
        )}
      </div>

      <div style={{padding: '15px', border: '1px dashed #666'}}>
        <h3>创建新匿名合集</h3>
        <form onSubmit={handleCreate}>
          {newEntries.map((entry, idx) => (
            <div key={idx} style={{marginBottom: '5px'}}>
              <input value={entry.path} onChange={e => { const n = [...newEntries]; n[idx].path = e.target.value; setNewEntries(n); }} placeholder="相对路径 (如 docs/a.txt)" style={{width: '300px'}} required />
              <input value={entry.hash} onChange={e => { const n = [...newEntries]; n[idx].hash = e.target.value; setNewEntries(n); }} placeholder="文件 Hash" style={{width: '300px'}} required />
            </div>
          ))}
          <button type="button" onClick={() => setNewEntries([...newEntries, { path: '', hash: '' }])} style={{marginRight:'10px'}}>+ 增加条目</button>
          <button type="submit" style={{background:'#000', color:'#fff', border:'none', padding:'5px 15px'}}>生成不可变合集</button>
        </form>
      </div>
    </div>
  );
}