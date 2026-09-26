// 首页（模块①）：节点搜索 / 连接（PeerJS 消费端）
// 搜索在线 peer（公共信令 discover）→ 点连接拨号 → 成功后跳转「节点控制」页；也可手动填 peer id。
import React from 'react';
import { useNavigate } from 'react-router-dom';
import PeerJSConnect from '../lib/PeerJSConnect';

export default function Connect() {
  const navigate = useNavigate();
  return (
    <div className="p-8 overflow-y-auto h-full">
      <div className="max-w-4xl mx-auto">
        <h1 className="text-2xl font-bold mb-1">节点搜索 / 连接</h1>
        <p className="text-sm text-gray-500 mb-6">
          搜索在线节点或填 peer id 连接；连接成功后进入节点控制页。
        </p>
        <div className="bg-white/[0.03] rounded-card border border-white/[0.06] p-5">
          <PeerJSConnect
            onConnected={() => navigate('/node')}
          />
        </div>
      </div>
    </div>
  );
}