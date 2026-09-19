export const LLM_URL = 'https://siliconflow.moonchan.xyz';
export const LLM_CHAT = `${LLM_URL}/v1/chat/completions`;

export const SEARCH_HISTORY_KEY = 'peerdrive_search_history';
export const MAX_HISTORY = 20;

export const SORT_OPTS = [
  { v: 'time', l: '时间' }, { v: 'name', l: '名称' },
  { v: 'path', l: '目录' }, { v: 'type', l: '类型' }, { v: 'size', l: '大小' },
];
export const TYPE_OPTS = [
  { v: '', l: '全部' }, { v: 'image/', l: '图片' }, { v: 'video/', l: '视频' },
  { v: 'audio/', l: '音频' }, { v: 'text/', l: '文本' },
];
export const COLL_SORT_OPTS = [
  { v: 'time', l: '时间' }, { v: 'name', l: '名称' }, { v: 'count', l: '文件数' },
];

// 三列布局 - 左侧面板来源标签
// 坑：曾一度精简成「本地电脑 / 合集」两项，导致中间两个文件视图失去入口——
// LeftPanel 里只有注释占位、列表区什么都不渲染（用户看到「已注册(按目录)是空的」）。
// 数据源口径见 index.jsx：registered = FileListItem.provider_path 非空。
export const SOURCE_TABS = [
  { id: 'local', label: '本地电脑' },
  { id: 'registered', label: '已注册·按文件' },
  { id: 'registered_dir', label: '已注册·按目录' },
  { id: 'collections', label: '合集' },
];

// 三列布局 - 左侧排序选项
// 坑：旧表里有 modified_at，但后端 FileListItem 只有 created_at（back model/file.go:34），
// 选「修改时间」时比较两侧都是 undefined → cmp 恒为 0，排序看起来完全失效。
export const LEFT_SORT_OPTS = [
  { v: 'created_at', l: '创建时间' },
  { v: 'name', l: '按文件名' },
  { v: 'size', l: '按大小' },
];

// 三列布局 - 左侧类型筛选（多选）
export const LEFT_TYPE_FILTERS = [
  { id: 'image/', label: '图片', icon: '🖼️' },
  { id: 'video/', label: '视频', icon: '🎬' },
  { id: 'audio/', label: '音频', icon: '🎵' },
  { id: 'text/', label: '文本', icon: '📝' },
  { id: 'document', label: '文档', icon: '📄' },
];
