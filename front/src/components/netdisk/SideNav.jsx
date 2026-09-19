// netdisk/SideNav.jsx：网盘左侧导航。
//
// 信息架构参考成熟网盘（Nextcloud Files 的左侧"文件"区 + Cloudreve 的
// 分组导航 + Alist 的简洁列表）：主区固定四个入口——我的网盘 / 节点市场 /
// 我的节点 / 传输任务，下面再挂"合集"与"高级"（BT/IPFS/设置等既有页面）。
//
// 为什么单独做侧栏而不是继续加顶部下拉：网盘的核心操作是"在文件与节点之间
// 来回切换"，侧栏常驻能把当前所在位置和可去的地方同时摆在眼前；顶部下拉
// 每次切换都要先展开一次。
import React from 'react';
import { NavLink } from 'react-router-dom';

const MAIN = [
  { to: '/drive', icon: '🗂', label: '我的网盘' },
  { to: '/market', icon: '🏪', label: '节点市场' },
  { to: '/peers', icon: '🔗', label: '我的节点' },
  { to: '/transfers', icon: '⬇', label: '传输任务' },
];

const ADVANCED = [
  { to: '/', icon: '📦', label: '合集广场' },
  { to: '/create', icon: '✏️', label: '创建合集' },
  { to: '/bt', icon: '🧲', label: 'BT 下载器' },
  { to: '/bt/dht', icon: '🌐', label: 'DHT 采样' },
  { to: '/ipfs', icon: '🪐', label: 'IPFS' },
  { to: '/settings', icon: '⚙', label: '设置' },
];

function Item({ to, icon, label }) {
  return (
    <NavLink
      to={to}
      end={to === '/'}
      className={({ isActive }) =>
        `flex items-center gap-2.5 px-3 py-2 rounded-md text-sm transition-colors ${
          isActive
            ? 'bg-blue-600/20 text-blue-300 border border-blue-700/40'
            : 'text-gray-400 hover:text-white hover:bg-gray-800 border border-transparent'
        }`
      }
    >
      <span className="w-5 text-center text-base leading-none">{icon}</span>
      <span className="truncate">{label}</span>
    </NavLink>
  );
}

export default function SideNav() {
  return (
    <aside className="hidden md:flex w-52 shrink-0 flex-col gap-1 border-r border-gray-800 bg-gray-900/60 p-3 overflow-y-auto">
      <div className="px-3 pb-1 pt-1 text-[10px] uppercase tracking-wider text-gray-600">网盘</div>
      {MAIN.map((it) => (
        <Item key={it.to} {...it} />
      ))}
      <div className="px-3 pb-1 pt-4 text-[10px] uppercase tracking-wider text-gray-600">内容与工具</div>
      {ADVANCED.map((it) => (
        <Item key={it.to} {...it} />
      ))}
    </aside>
  );
}

// MobileNav 小屏下的横向入口条（侧栏在小屏隐藏，但不能把入口一起藏了）。
export function MobileNav() {
  return (
    <div className="md:hidden flex gap-2 overflow-x-auto border-b border-gray-800 bg-gray-900/60 px-3 py-2">
      {MAIN.map((it) => (
        <NavLink
          key={it.to}
          to={it.to}
          className={({ isActive }) =>
            `shrink-0 inline-flex items-center gap-1.5 px-3 py-1.5 rounded-full text-xs border ${
              isActive
                ? 'bg-blue-600/20 text-blue-300 border-blue-700/40'
                : 'text-gray-400 border-gray-700'
            }`
          }
        >
          <span>{it.icon}</span>
          {it.label}
        </NavLink>
      ))}
    </div>
  );
}
