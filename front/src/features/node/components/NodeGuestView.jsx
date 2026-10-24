import React from 'react';
import { Link } from 'react-router-dom';

export default function NodeGuestView() {
  return (
    <div className="p-8 max-w-xl mx-auto text-center py-20">
      <div className="w-12 h-12 rounded-full bg-amber-500/20 text-amber-300 flex items-center justify-center mx-auto mb-4 text-xl">
        🔒
      </div>
      <h2 className="text-lg font-bold text-gray-200 mb-2">机身控制面仅对本地属主开放</h2>
      <p className="text-sm text-gray-400 mb-6 leading-relaxed">
        您当前通过 WebRTC 远程连入节点（访客模式）。机身底层配置、网络端口与服务启停由属主统一管理。您可以前往“合集与网盘”浏览并拉取公开分享的资源。
      </p>
      <div className="flex justify-center gap-3">
        <Link to="/collections" className="px-4 py-2 bg-brand-600 hover:bg-brand-500 text-white rounded-lg text-xs font-medium transition-colors">
          查看公开合集 (Collections)
        </Link>
        <Link to="/" className="px-4 py-2 bg-white/[0.08] hover:bg-white/[0.12] text-gray-300 rounded-lg text-xs transition-colors">
          返回节点广场 (Plaza)
        </Link>
      </div>
    </div>
  );
}
