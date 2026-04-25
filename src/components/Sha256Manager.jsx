import React, { useState } from 'react';
import * as api from '../api';

export default function Sha256Manager() {
  const [hash, setHash] = useState('');
  const [meta, setMeta] = useState(null);

  const handleVerify = async (e) => {
    e.preventDefault();
    try {
      const res = await api.verifyFile(hash);
      setMeta(res);
    } catch {
      setMeta({ error: '未找到' });
    }
  };

  return (
    <div>
      <h2>SHA256 寻址</h2>
      <form onSubmit={handleVerify}>
        <input value={hash} onChange={e => setHash(e.target.value)} placeholder="输入文件 Hash" style={{width: '500px'}} />
        <button type="submit">查询元数据</button>
      </form>
      
      {meta && !meta.error && (
        <div style={{marginTop: '20px', background: '#f4f4f4', padding: '15px'}}>
          <pre>{JSON.stringify(meta, null, 2)}</pre>
          <a href={api.getDownloadUrl(hash)} target="_blank" rel="noreferrer" style={{display:'inline-block', marginTop:'10px', padding:'8px', background:'#000', color:'#fff', textDecoration:'none'}}>
            下载文件
          </a>
        </div>
      )}
      {meta?.error && <p style={{color:'red'}}>{meta.error}</p>}
    </div>
  );
}