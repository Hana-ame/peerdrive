// 合集可见性档位常量 —— 必须与后端 back/internal/model/anon.go 的三个字符串一一对应。
//
// 为什么单独放一个文件而不是放 api.js：
// 测试里 `vi.mock('../src/api.js')` 是全量 automock（tests/setup.js，用来掐掉组件
// useEffect 里的真请求），非函数的导出会被替换掉 → 组件里写 `api.VISIBILITY.PUBLIC`
// 会在渲染时直接抛 "Cannot read properties of undefined"。
// 常量属于纯数据，放这里既能被 api 层复用，又不会被 mock 影响。
export const VISIBILITY_PUBLIC = 'public';
export const VISIBILITY_RESTRICTED = 'restricted';
export const VISIBILITY_PRIVATE = 'private';

export const VISIBILITY = {
  PUBLIC: VISIBILITY_PUBLIC,
  RESTRICTED: VISIBILITY_RESTRICTED,
  PRIVATE: VISIBILITY_PRIVATE,
};

// 供列表/详情页展示的中文标签与图标（与 VisibilityPicker 的三选项文案保持一致）
export const VISIBILITY_META = {
  [VISIBILITY_PUBLIC]: { icon: '🌐', label: '公开访问' },
  [VISIBILITY_RESTRICTED]: { icon: '👥', label: '仅限权限' },
  [VISIBILITY_PRIVATE]: { icon: '🔒', label: '仅自己' },
};

// visibilityMeta 兜底：历史集合没有该字段时后端已回填 public，这里再兜一层防脏数据。
export function visibilityMeta(v) {
  return VISIBILITY_META[v] || VISIBILITY_META[VISIBILITY_PUBLIC];
}

export default VISIBILITY;
