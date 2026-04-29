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
