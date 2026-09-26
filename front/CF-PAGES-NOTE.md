# Cloudflare Pages 构建说明（peerdrive 前端）

生产部署依赖：CF 面板 Pages 项目 `peerdrive` → Settings：
- Production branch: `refactor`
- Build configurations: Root `front` / Build `npm ci && npm run build` / Output `dist`
- Build watch paths (Build triggers → Include paths): `*`（或 `front/*`）

push 到 refactor 且命中 include paths 才会触发生产构建；空提交（无文件变更）会被 watch 跳过。
