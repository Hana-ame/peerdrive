// Pure i18n translation dictionary & helper for Peerdrive UI
export const DICTIONARY = {
  zh: {
    'lang.name': '中文',
    'lang.toggle': 'EN',
    // Nav
    'nav.connect': '节点广场',
    'nav.display': '大屏投屏',
    'nav.drive': '我的网盘',
    'nav.node': '节点控制',
    'nav.collections': '合集管理',
    'nav.transfers': '传输列表',
    'nav.bt': 'BT 种子',
    'nav.ipfs': 'IPFS 存储',
    'nav.iwara': 'Iwara 媒体',
    'nav.settings': '节点设置',
    'nav.guest': '访客',
    // Connect page
    'connect.title': '节点搜索与互联',
    'connect.subtitle': '搜索公网在线节点或输入节点 ID 进行连接；连接成功后可直接浏览与保存共享内容。',
    'connect.localOnline': '本地存储节点运行中',
    'connect.localDesc': '本地节点已连接，已加载测试数据与合集。可直接进入网盘浏览文件列表与图片缩略图。',
    'connect.goToDrive': '进入我的网盘 (Drive)',
    'connect.goToCollections': '查看合集 (Collections)',
    'connect.searchNodes': '搜索在线节点',
    'connect.refresh': '刷新',
    'connect.searching': '搜索中…',
    'connect.onlineCount': '{count} 个在线节点',
    'connect.noNodes': '当前暂无在线节点 (节点上线后将通过信令广播自动呈现)',
    'connect.cardView': '卡片视图',
    'connect.listView': '列表视图',
    'connect.advSig': '高级信令设置',
    'connect.sigHost': '信令主机 (Host)',
    'connect.sigPort': '端口 (Port)',
    'connect.sigKey': 'API Key',
    'connect.targetPeer': '目标节点 ID (输入呼叫目标)',
    'connect.pskPlaceholder': 'PSK 密钥 (可选 / 留空为游客)',
    'connect.connectBtn': '连接节点',
    'connect.disconnectBtn': '断开连接',
    'connect.connecting': '正在建立 WebRTC 连接…',
    'connect.connected': '已连接 ✓',
    'connect.failed': '连接失败: ',
    'connect.peerShares': '对端共享资源',
    'connect.save': '保存',
    // Card
    'card.online': '在线',
    'card.open': '公开免密',
    'card.psk': '需密码 (PSK)',
    'card.collections': '{count} 个公开合集',
    'card.noShares': '暂无公开内容',
    'card.caps': '能力',
    'card.changeBanner': '换横幅',
    'card.copyId': '复制 ID',
    'card.copied': '已复制 ✓',
    // Banner modal
    'banner.modalTitle': '设置节点横幅 (Banner)',
    'banner.presetThemes': '精选主题预设',
    'banner.customUrl': '或输入自定义图片 URL',
    'banner.preview': '横幅效果预览',
    'banner.save': '应用横幅',
    'banner.cancel': '取消',
    'banner.reset': '恢复默认',
  },
  en: {
    'lang.name': 'English',
    'lang.toggle': '中',
    // Nav
    'nav.connect': 'Connect',
    'nav.display': 'Display',
    'nav.drive': 'Drive',
    'nav.node': 'Node',
    'nav.collections': 'Collections',
    'nav.transfers': 'Transfers',
    'nav.bt': 'BT',
    'nav.ipfs': 'IPFS',
    'nav.iwara': 'Iwara',
    'nav.settings': 'Settings',
    'nav.guest': 'Guest',
    // Connect page
    'connect.title': 'Node Search / Connect',
    'connect.subtitle': 'Search online nodes or enter a peer id to connect; on successful connection, browse and save shared content.',
    'connect.localOnline': 'Local Storage Node Online',
    'connect.localDesc': 'Local node is active with test data and collections. Access drive directly to view files and thumbnails.',
    'connect.goToDrive': 'Go to My Drive',
    'connect.goToCollections': 'View Collections',
    'connect.searchNodes': 'Search Online Nodes',
    'connect.refresh': 'Refresh',
    'connect.searching': 'Searching...',
    'connect.onlineCount': '{count} online node(s)',
    'connect.noNodes': 'No online nodes currently (nodes will be announced via signaling when online).',
    'connect.cardView': 'Card View',
    'connect.listView': 'List View',
    'connect.advSig': 'Signaling Settings',
    'connect.sigHost': 'Signaling Host',
    'connect.sigPort': 'Port',
    'connect.sigKey': 'API Key',
    'connect.targetPeer': 'Target peer id (dial target)',
    'connect.pskPlaceholder': 'PSK key (optional / guest if empty)',
    'connect.connectBtn': 'Connect',
    'connect.disconnectBtn': 'Disconnect',
    'connect.connecting': 'Connecting...',
    'connect.connected': 'Connected ✓',
    'connect.failed': 'Connection failed: ',
    'connect.peerShares': 'Peer Shares',
    'connect.save': 'Save',
    // Card
    'card.online': 'Online',
    'card.open': 'Open Access',
    'card.psk': 'Password (PSK)',
    'card.collections': '{count} collection(s)',
    'card.noShares': 'No public shares',
    'card.caps': 'Caps',
    'card.changeBanner': 'Change Banner',
    'card.copyId': 'Copy ID',
    'card.copied': 'Copied ✓',
    // Banner modal
    'banner.modalTitle': 'Customize Node Banner',
    'banner.presetThemes': 'Preset Theme Banners',
    'banner.customUrl': 'Or enter custom image URL',
    'banner.preview': 'Banner Preview',
    'banner.save': 'Apply Banner',
    'banner.cancel': 'Cancel',
    'banner.reset': 'Reset Default',
  },
};

/**
 * Format string replacing {key} placeholders
 */
export function translate(lang, key, params = {}) {
  const dict = DICTIONARY[lang] || DICTIONARY.zh;
  let text = dict[key] || DICTIONARY.en?.[key] || key;
  if (params && typeof params === 'object') {
    for (const [k, v] of Object.entries(params)) {
      text = text.replace(new RegExp(`\\{${k}\\}`, 'g'), String(v));
    }
  }
  return text;
}
