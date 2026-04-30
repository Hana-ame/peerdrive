// 合集评论组件：查看和发布评论
import React, { useState, useEffect } from 'react';
import * as api from '../api';

function formatTime(ts) {
  if (!ts) return '';
  const d = new Date(ts);
  const now = new Date();
  const diff = Math.floor((now - d) / 1000);
  if (diff < 60) return '刚刚';
  if (diff < 3600) return `${Math.floor(diff / 60)} 分钟前`;
  if (diff < 86400) return `${Math.floor(diff / 3600)} 小时前`;
  if (diff < 604800) return `${Math.floor(diff / 86400)} 天前`;
  return d.toLocaleDateString();
}

export default function CommentSection({ hash }) {
  const [comments, setComments] = useState([]);
  const [content, setContent] = useState('');
  const [loading, setLoading] = useState(true);
  const [posting, setPosting] = useState(false);
  const [error, setError] = useState('');
  const [regServerUrl, setRegServerUrl] = useState('');

  useEffect(() => {
    const url = localStorage.getItem('peerdrive_reg_server') || '';
    setRegServerUrl(url);
  }, []);

  const fetchComments = async () => {
    if (!regServerUrl || !hash) return;
    setLoading(true);
    setError('');
    try {
      const data = await api.getComments(regServerUrl, hash);
      setComments(data.comments || []);
    } catch (e) {
      setError(e.message);
      setComments([]);
    }
    setLoading(false);
  };

  useEffect(() => {
    if (regServerUrl && hash) {
      fetchComments();
    }
  }, [regServerUrl, hash]);

  // Refresh when hash changes
  useEffect(() => {
    setComments([]);
    setContent('');
    setError('');
    if (regServerUrl && hash) {
      fetchComments();
    }
  }, [hash]);

  const getToken = () => {
    const fragmentToken = localStorage.getItem('peerdrive_auth_token');
    if (fragmentToken) return fragmentToken;
    if (localStorage.getItem('peerdrive_auth_header_enabled') === 'true') {
      return localStorage.getItem('peerdrive_auth_key') || '';
    }
    return '';
  };

  const handlePost = async () => {
    const text = content.trim();
    if (!text) return;
    const token = getToken();
    if (!token) {
      setError('请先在设置页面登录并启用 Token 认证');
      return;
    }

    setPosting(true);
    setError('');
    try {
      const comment = await api.postComment(regServerUrl, hash, text, token);
      setComments(prev => [...prev, comment]);
      setContent('');
    } catch (e) {
      setError(e.message);
    }
    setPosting(false);
  };

  if (!regServerUrl) {
    return null; // no reg server configured, hide comments
  }

  return (
    <div className="border-t border-gray-700/50 mt-4 pt-4">
      <h3 className="text-sm font-semibold text-gray-300 mb-3 flex items-center gap-2">
        <span>💬</span> 评论 ({comments.length})
      </h3>

      {/* Post Form */}
      <div className="flex gap-2 mb-4">
        <input
          value={content}
          onChange={e => setContent(e.target.value)}
          onKeyDown={e => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); handlePost(); } }}
          placeholder="发表评论（需已认证）..."
          className="flex-1 bg-gray-800 border border-gray-600 px-3 py-2 rounded text-sm focus:outline-none focus:border-blue-500"
          disabled={posting}
        />
        <button
          onClick={handlePost}
          disabled={posting || !content.trim()}
          className="bg-blue-600 hover:bg-blue-700 disabled:opacity-40 px-4 py-2 rounded text-sm font-medium transition-colors"
        >
          {posting ? '...' : '发送'}
        </button>
      </div>

      {error && <p className="text-xs text-red-400 mb-2">{error}</p>}

      {/* Comment List */}
      {loading ? (
        <p className="text-xs text-gray-500 text-center py-4">加载评论中...</p>
      ) : comments.length === 0 ? (
        <p className="text-xs text-gray-500 text-center py-4">暂无评论</p>
      ) : (
        <div className="space-y-3 max-h-80 overflow-y-auto">
          {comments.map((c, i) => (
            <div key={c.id || i} className="bg-gray-800/60 rounded-lg px-3 py-2.5 border border-gray-700/40">
              <div className="flex items-center justify-between mb-1">
                <span className="text-xs font-medium text-blue-400">{c.username}</span>
                <span className="text-[10px] text-gray-500">{formatTime(c.created_at)}</span>
              </div>
              <p className="text-sm text-gray-300 break-words">{c.content}</p>
            </div>
          ))}
        </div>
      )}

      {/* Refresh button */}
      <button
        onClick={fetchComments}
        className="text-[10px] text-gray-500 hover:text-gray-300 mt-2 transition-colors"
        disabled={loading}
      >
        {loading ? '刷新中...' : '🔄 刷新评论'}
      </button>
    </div>
  );
}
