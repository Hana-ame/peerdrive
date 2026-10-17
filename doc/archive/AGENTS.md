# AGENTS.md — Peerdrive Frontend (React)

## Important Rules

### 1. Cloudflare Pages 部署
- **构建由 Cloudflare 执行**，不是在本地。所有构建配置文件必须在 git 中，否则 Cloudflare 看不到。
- **sourcemap 必须传**：`vite.config.ts` 中已设置 `build: { sourcemap: true }`。这个配置项若丢失会导致生产环境 debug 无法溯源。
- **SPA fallback**：`public/_redirects` 内容为 `/* /index.html 200`。此文件必须存在于 git。
- **dist/ 已 gitignore**，不要手动提交构建产物。
- **每次代码推送到 `frontend` 分支后** Cloudflare 自动构建部署。

### 2. Git 注意事项
- 工作目录是 monorepo 根目录 `/mnt/d/WorkPlace/peerdrive`，但前端代码在 `react/` 子目录中（独立 git repo）。
- 前端分支：`frontend`，远程 `origin/frontend`。
- **新建文件必须 git add**，尤其 `vite.config.ts`、`_redirects`、`public/` 下的静态资源。
- commit message 不要留空。

### 3. 技术栈
- **Vite** + **React** (JSX) + **Tailwind CSS** + **React Router**
- Node >= 22.12 或 >= 20.19（Vite 8 要求）
- API 封装在 `src/api.js`，默认 `API_BASE` 从 `localStorage` 读取。

### 4. LLM 助手 (LLMAssistant.jsx)
- 使用 `siliconflow.moonchan.xyz` 代理，免费模型无需 API Key。
- 支持 function calling（17 个 tool：navigate_to、文件管理、合集操作等）。
- 流式 SSE 读取 + tool_calls 增量累积。
- 多轮对话最多 5 轮，使用本地 `accMessages` 数组避免闭包 stale state 问题。

### 5. 已知陷阱
- **const TDZ**：`useEffect` 依赖数组中的变量必须在 effect 之前声明，否则运行时报 `Cannot access 'x' before initialization`。
- **useState 闭包**：callback 内直接引用 state 变量在多轮异步中可能读到旧值，需用本地累加变量。
