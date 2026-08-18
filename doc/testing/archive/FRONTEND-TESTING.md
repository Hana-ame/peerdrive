# Peerdrive 前端测试文档

> Vitest + Happy DOM + Testing Library — 单元测试 · 组件测试 · 冒烟测试
> 更新: 2026-04-30

---

## 目录

1. [测试架构](#1-测试架构)
2. [环境配置](#2-环境配置)
3. [测试文件组织](#3-测试文件组织)
4. [运行测试](#4-运行测试)
5. [编写测试](#5-编写测试)
6. [现有测试覆盖](#6-现有测试覆盖)
7. [Playwright E2E](#7-playwright-e2e)
8. [测试清单](#8-测试清单)

---

## 1. 测试架构

```
┌──────────────────────────────────────────────┐
│                 Vitest (Runner)               │
│  ┌─────────────────────────────────────────┐ │
│  │         Happy DOM (Browser Env)          │ │
│  │  ┌─────────────────────────────────────┐ │ │
│  │  │    Testing Library / React           │ │ │
│  │  │    render() → screen.getByText()...  │ │ │
│  │  └─────────────────────────────────────┘ │ │
│  └─────────────────────────────────────────┘ │
└──────────────────────────────────────────────┘
```

| 层级 | 工具 | 职责 |
|------|------|------|
| 测试运行器 | Vitest 4 | 执行测试、断言、覆盖率 |
| DOM 环境 | Happy DOM | 模拟浏览器 DOM API (无头) |
| React 渲染 | @testing-library/react | `render()` 组件到虚拟 DOM |
| 断言扩展 | @testing-library/jest-dom | `.toBeTruthy()`, `.toContain()` 等 |
| 路由模拟 | react-router-dom MemoryRouter | 包裹需要路由上下文的组件 |

### 为什么选 Happy DOM？

- 比 jsdom 更快、更轻量
- 对现代 Web API 支持更好
- 与 Vitest 集成无需额外配置

---

## 2. 环境配置

### vitest.config.ts (`front/vitest.config.ts`)

```ts
import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],           // 处理 JSX 转换
  test: {
    environment: 'happy-dom',   // 浏览器环境模拟
    setupFiles: ['./tests/setup.js'],  // 全局 setup
  },
})
```

### 全局 setup (`front/tests/setup.js`)

```js
import { afterEach } from 'vitest'
import { cleanup } from '@testing-library/react'

afterEach(() => {
  cleanup()  // 每个测试后卸载组件，防止状态泄漏
})
```

### 依赖版本

```json
{
  "devDependencies": {
    "vitest": "^4.1.5",
    "happy-dom": "^20.9.0",
    "@happy-dom/global-registrator": "^20.9.0",
    "@testing-library/react": "^16.3.2",
    "@testing-library/jest-dom": "^6.9.1",
    "@vitejs/plugin-react": "^6.0.1"
  }
}
```

---

## 3. 测试文件组织

```
front/tests/
├── setup.js              # 全局 setup (afterEach cleanup)
├── smoke.test.jsx        # 冒烟测试：核心组件能否渲染
├── components.test.jsx   # 组件渲染测试：各页面/组件基本渲染
└── FileTree.test.jsx     # FileTree 专项测试：状态、交互
```

### 命名约定

| 文件 | 测试内容 |
|------|---------|
| `*.test.jsx` | Vitest 单元/组件测试 |
| `*.smoke.mjs` | Node.js 冒烟脚本（直接执行） |

---

## 4. 运行测试

```bash
cd front

# 运行所有测试（单次）
npm test

# 等价于
npx vitest run

# Watch 模式（文件变更自动重跑）
npx vitest

# 运行特定文件
npx vitest run tests/FileTree.test.jsx

# 运行匹配名称的测试
npx vitest run -t "FileTree"

# 生成覆盖率报告
npx vitest run --coverage

# UI 模式（可视化界面）
npx vitest --ui
```

---

## 5. 编写测试

### 5.1 基本模式

```jsx
import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import MyComponent from '../src/components/MyComponent'

// 包裹路由上下文的 wrapper
const wrapper = ({ children }) => <MemoryRouter>{children}</MemoryRouter>

describe('MyComponent', () => {
  it('renders title', () => {
    render(<MyComponent />, { wrapper })
    expect(screen.getByText('标题')).toBeTruthy()
  })

  it('renders button', () => {
    render(<MyComponent />, { wrapper })
    expect(screen.getByRole('button', { name: '提交' })).toBeTruthy()
  })
})
```

### 5.2 查询方法速查

| 方法 | 用途 | 失败行为 |
|------|------|---------|
| `screen.getByText('xxx')` | 精确文本匹配 | 找不到抛错 |
| `screen.getByPlaceholderText('xxx')` | 按 placeholder 查找 | 找不到抛错 |
| `screen.getByRole('button', { name: 'xxx' })` | 按 ARIA 角色查找 | 找不到抛错 |
| `screen.queryByText('xxx')` | 文本匹配 | 找不到返回 null |
| `container.textContent` | 获取元素内所有文本 | - |

### 5.3 不用 Router 的组件

对于不依赖路由的纯展示组件，直接 `render()` 即可：

```jsx
import { render } from '@testing-library/react'
import FileTree from '../src/components/FileTree'

it('renders empty state', () => {
  const { container } = render(
    <FileTree entries={[]} entryActions={{}} />
  )
  expect(container.textContent).toContain('拖拽')
})
```

### 5.4 需要 Context 的组件

如果组件消费 AppContext 或 PageContext，需要在测试中包裹 Provider：

```jsx
import { AppContext, PageContext } from '../src/App'

function Wrapper({ children }) {
  return (
    <AppContext.Provider value={{ username: 'test', setUsername: () => {}, nodeInfo: null, setNodeInfo: () => {} }}>
      <PageContext.Provider value={{ pageContext: null, setPageContext: () => {} }}>
        <MemoryRouter>{children}</MemoryRouter>
      </PageContext.Provider>
    </AppContext.Provider>
  )
}

render(<MyPage />, { wrapper: Wrapper })
```

### 5.5 测试原则

1. **渲染测试优先** — 每个组件至少要能渲染不崩溃
2. **关键文本断言** — 检查标题/按钮/占位符是否出现
3. **边界状态** — 空数据、loading、error 状态
4. **不测实现细节** — 不测 state 内部值、不测 CSS 类名
5. **隔离** — 每个测试不依赖其他测试的状态

---

## 6. 现有测试覆盖

### 冒烟测试 (`tests/smoke.test.jsx`)

确保核心组件不崩溃：

| 组件 | 断言 |
|------|------|
| `Sha256Manager` | 标题 "SHA256 寻址" |
| `CollectionBuilder` | 标题 "合集构建器" + "浏览合集" |
| `P2PStatus` | 标题 "P2P 网络" |
| `App` | Logo "Peerdrive" |

### 组件测试 (`tests/components.test.jsx`)

覆盖主要页面和组件的渲染：

| 测试对象 | 测试数 | 关键断言 |
|----------|--------|---------|
| `App` | 1 | Navbar 中 Peerdrive logo |
| `Plaza` | 3 | 标题/创建按钮/搜索框 |
| `AnonCreator` | 3 | 4-Tab 栏/保存按钮/无 Commit 按钮 |
| `AnonExplorer` | 1 | Hash 输入框 |
| `FileManager` | 1 | 标题 "文件管理" |
| `Settings` | 1 | 标题 "设置" |
| `VersionLog` | 1 | 空状态提示 |
| `Navbar` | 1 | 导航链接 |

### FileTree 专项测试 (`tests/FileTree.test.jsx`)

| 测试 | 覆盖场景 |
|------|---------|
| 空状态渲染 | `entries=[]` → 显示 "拖拽" |
| 平铺条目渲染 | 单个文件 → 显示文件名 |
| 嵌套路径展开 | `d/a.txt` → 显示目录 "d" |
| 新建文件夹按钮 | 确认按钮文本出现 |

---

## 7. Playwright E2E

### 冒烟脚本 (`tests/playwright-smoke.mjs`)

这是一个独立的 Node.js 脚本，用于端到端验证——连接到 Windows 宿主机上运行的浏览器（CDP 端口 9222），测试真实的页面交互。

```bash
# 前提：Windows 宿主机 Chrome/Edge 启动时带调试端口
# chrome.exe --remote-debugging-port=9222 --remote-debugging-address=0.0.0.0

cd front
node tests/playwright-smoke.mjs
```

该脚本需要 Playwright 技能 (`playwright-test`) 才能运行。详见对应技能文档。

---

## 8. 测试清单

### 核心页面渲染

- [ ] `App` — 完整布局渲染
- [ ] `Plaza` — 广场加载、合集卡片展示
- [ ] `FileManager` — 文件列表展示
- [ ] `Explorer` — 合集详情展示
- [ ] `AnonCreator` — 三栏布局渲染
- [ ] `AnonExplorer` — 合集浏览渲染
- [ ] `Settings` — 设置分组渲染
- [ ] `P2PPanel` — P2P 状态展示
- [ ] `IPFSPanel` — IPFS 面板渲染
- [ ] `BTPanel` — BT 面板渲染

### 组件专项

- [ ] `FileTree` — 空/平铺/嵌套/多文件
- [ ] `Navbar` — 搜索面板打开/关闭、键盘导航
- [ ] `MobileNav` — 移动端链接渲染
- [ ] `LLMAssistant` — 对话窗口渲染
- [ ] `VersionLog` — 版本列表/空状态/回滚按钮
- [ ] `CommentSection` — 评论列表/发表
- [ ] `CollectionCard` — 卡片内容展示
- [ ] `VisibilityPicker` — 可见性选项
- [ ] `WebRTCTransfer` — 传输进度展示

### 交互与状态

- [ ] 用户输入 → 状态更新
- [ ] API 调用成功 → UI 更新
- [ ] API 调用失败 → 错误提示
- [ ] Loading 状态 → 骨架屏/加载指示器
- [ ] 空数据状态 → 空状态提示
- [ ] 路由跳转 → 页面切换

### 建议添加的测试

以下测试尚未实现，建议按优先级补充：

1. **API mock 测试** — 用 `vi.mock()` 或 `msw` mock 后端响应，测试加载和错误状态
2. **用户交互测试** — 使用 `fireEvent` 或 `@testing-library/user-event` 测试按钮点击、表单输入
3. **AnonCreator 交互** — 测试三栏拖拽、文件选择、合集保存流程
4. **快照测试** — 对关键组件做快照对比，防止意外 UI 变更

---

## 附录：常用断言速查

```jsx
// 存在性
expect(screen.getByText('标题')).toBeTruthy()
expect(screen.queryByText('不应存在')).toBeNull()

// 文本内容
expect(container.textContent).toContain('部分文本')
expect(element).toHaveTextContent('精确文本')

// 属性
expect(input).toHaveValue('test')
expect(link).toHaveAttribute('href', '/target')

// 可见性
expect(element).toBeVisible()
expect(element).not.toBeVisible()
```
