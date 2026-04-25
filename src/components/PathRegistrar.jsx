import React, { useState } from 'react';
import * as api from '../api';

export default function PathRegistrar({ onHashGenerated }) {
  const [path, setPath] = useState('');
  const [result, setResult] = useState(null);

  const handleRegister = async (e) => {
    e.preventDefault();
    try {
      const res = await api.registerLocalFile(path);
      setResult(res);
    } catch (err) {
      alert('注册失败: ' + (err.message || 'unknown error'));
    }
  };

  return (
    <div>
      <h2>注册本地 Path</h2>
      <p>将服务器上已存在的文件纳入系统，零拷贝。</p>
      <form onSubmit={handleRegister}>
        <input value={path} onChange={e => setPath(e.target.value)} placeholder="服务器绝对路径 (如 /mnt/nas/video.mp4)" style={{width: '500px'}} required />
        <button type="submit">注册</button>
      </form>

      {result && (
        <div style={{marginTop: '20px', background: '#e8f5e9', padding: '15px'}}>
          <p>注册成功！Hash: <strong>{result.hash}</strong></p>
          <button onClick={() => onHashGenerated(result.hash)} style={{padding:'8px', background:'#4CAF50', color:'#fff', border:'none', cursor:'pointer'}}>
            用此 Hash 创建匿名合集 →
          </button>
        </div>
      )}
    </div>
  );
}