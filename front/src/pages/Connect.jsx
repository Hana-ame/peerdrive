// 首页（模块①）：节点搜索 / 连接（PeerJS 消费端）
// 搜索在线 peer（公共信令 discover）→ 点连接拨号 → 看共享/保存；也可手动填 peer id。
import React from 'react';
import PeerJSConnect from '../lib/PeerJSConnect';

export default function Connect() {
  return (
    <div className="p-8 overflow-y-auto h-full">
      <div className="max-w-4xl mx-auto">
        <h1 className="text-2xl font-bold mb-1">节点搜索 / 连接</h1>
        <p className="text-sm text-gray-500 mb-6">
          搜索在线节点或填 peer id，经公共信令（peersignal.moonchan.xyz）拨号连接；连上后查看并保存对方共享的文件/合集。
        </p>
        <div className="bg-white/[0.03] rounded-card border border-white/[0.06] p-5">
          <PeerJSConnect />
        </div>
      </div>
    </div>
  );
}