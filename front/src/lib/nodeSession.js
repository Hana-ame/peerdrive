// nodeSession.js — connected peer node session singleton.
//
// 为什么必须保持模块级全局态：
// 1. WebRTC DataConnection/Client 引用需要在页面路由间无缝交接（如 Connect 路由完成信令建立后，
//    用户导航至 NodeControl 管理或触发 swBridge 流传输），不能随单页组件生命周期的卸载而被销毁。
// 2. 生命期与所有权约定：
//    - 连接由 Connect 页面（或相关入口）创建并注入 setNodeSession({ client, peerId })。
//    - 活跃期供 NodeControl 页面及 swBridge 读取使用。
//    - 销毁期：调用 clearNodeSession() 具有直接副作用，会主动触发 session.client.close() 关闭底层
//      WebRTC 连接并将引用置空；该方法保证异常吞吐与重复调用的幂等性。
//
// 待未来架构重构（如引入专职的全局 Session Store / Context）后再行解耦，避免在页面迁移期间引入回归风险。

let session = null; // { client, peerId }

export function setNodeSession(s) {
  session = s;
}

export function getNodeSession() {
  return session;
}

export function clearNodeSession() {
  if (session && session.client && typeof session.client.close === 'function') {
    try { session.client.close(); } catch (e) { /* ignore */ }
  }
  session = null;
}