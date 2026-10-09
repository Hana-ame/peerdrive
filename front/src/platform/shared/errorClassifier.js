// errorClassifier.js — Classify errors and provide actionable fallback guidance.
//
// 为什么需要错误分类（issue #149）：
// 1. 冷启动（#71）和丢帧超时（#72）发生时，统一的"加载失败"无法引导用户解决问题。
// 2. 区分 5 大类错误：
//    - disconnected: 未连接本地节点 / WebSocket 断开
//    - unreachable: 远程节点不可达 / 对端离线
//    - unauthorized: 权限不足 / PSK 门禁拦截
//    - timeout: 请求超时 / 丢帧挂起
//    - missing: 数据缺失 / 404 / 条目不存在
//    - unknown: 未知错误
// 3. 针对每种错误提供具体的"降级建议"和引导动作。

export const ERROR_CATEGORIES = {
  DISCONNECTED: 'disconnected',
  UNREACHABLE: 'unreachable',
  UNAUTHORIZED: 'unauthorized',
  TIMEOUT: 'timeout',
  MISSING: 'missing',
  UNKNOWN: 'unknown',
};

export function classifyError(error) {
  const msg = String(error?.message || error || '').toLowerCase();

  // 1. 未连接
  if (
    msg.includes('not connected') ||
    msg.includes('connection closed') ||
    msg.includes('websocket') ||
    msg.includes('ws connecting') ||
    msg.includes('failed to fetch') ||
    msg.includes('network error')
  ) {
    return {
      category: ERROR_CATEGORIES.DISCONNECTED,
      title: 'Node Disconnected',
      message: 'Not connected to local node or WebSocket session dropped.',
      suggestion: 'Check if peerdrive daemon is running locally, then click Retry.',
      icon: '🔌',
    };
  }

  // 2. 权限不足 / PSK 拦截
  if (
    msg.includes('psk_required') ||
    msg.includes('forbidden') ||
    msg.includes('unauthorized') ||
    msg.includes('401') ||
    msg.includes('403') ||
    msg.includes('permission')
  ) {
    return {
      category: ERROR_CATEGORIES.UNAUTHORIZED,
      title: 'Access Restricted',
      message: 'Access requires authentication or pre-shared key (PSK).',
      suggestion: 'Configure PEERDRIVE_PSK or enter access credentials in Settings.',
      icon: '🔒',
    };
  }

  // 3. 超时
  if (
    msg.includes('timeout') ||
    msg.includes('timed out') ||
    msg.includes('hang') ||
    msg.includes('aborted')
  ) {
    return {
      category: ERROR_CATEGORIES.TIMEOUT,
      title: 'Request Timed Out',
      message: 'The request took too long to respond.',
      suggestion: 'The peer or local daemon might be busy. Please try again.',
      icon: '⏱️',
    };
  }

  // 4. 节点不可达
  if (
    msg.includes('peer unreachable') ||
    msg.includes('could not connect') ||
    msg.includes('dial') ||
    msg.includes('no peer') ||
    msg.includes('peer unavailable')
  ) {
    return {
      category: ERROR_CATEGORIES.UNREACHABLE,
      title: 'Peer Unreachable',
      message: 'Remote node is offline or NAT traversal failed.',
      suggestion: 'Try connecting to another node in the Node Directory / Market.',
      icon: '📡',
    };
  }

  // 5. 资源不存在 / 缺失
  if (
    msg.includes('not found') ||
    msg.includes('404') ||
    msg.includes('missing') ||
    msg.includes('does not exist') ||
    msg.includes('no entries')
  ) {
    return {
      category: ERROR_CATEGORIES.MISSING,
      title: 'Resource Not Found',
      message: 'The requested file, manifest, or collection does not exist.',
      suggestion: 'Verify the hash / ID or pull the content from another peer.',
      icon: '🔍',
    };
  }

  return {
    category: ERROR_CATEGORIES.UNKNOWN,
    title: 'Operation Failed',
    message: String(error?.message || error || 'An unexpected error occurred.'),
    suggestion: 'Review system logs or retry the operation.',
    icon: '⚠️',
  };
}
