// 移动端底部导航栏（md 断点以下可见）
import React from 'react';
import { useLocation, useNavigate } from 'react-router-dom';

const tabs = [
  { path: '/', label: 'Home', icon: '🏠' },
  { path: '/', label: 'Plaza', icon: '🏠' },
  { path: '/create', label: 'Create', icon: '➕' },
  { path: '/p2p', label: 'P2P', icon: '🌐' },
  { path: '/settings', label: 'Settings', icon: '⚙' },
];

export default function MobileNav() {
  return null;
}
