import { defineConfig, loadEnv } from 'vite'
import react from '@vitejs/plugin-react'
import path from 'path'
import { fileURLToPath } from 'url'

const __dirname = path.dirname(fileURLToPath(import.meta.url))

// https://vite.dev/config/
export default defineConfig(({ mode }) => {
  // envDir 用 __dirname 而不是 '.'：从仓库根跑 `vite --config front/vite.config.ts`
  // 时 cwd 是根，相对路径会读错目录。
  const env = loadEnv(mode, __dirname, '')

  // HMR 客户端端口，只在“外部只能访问 443”的反代环境里才需要改写。
  //
  // 背景：Cloud Shell 这类 Web IDE 把 dev server 的 8080 反代到公网 443，
  // 页面本身能打开，但 Vite 注入的 HMR 客户端默认仍连 server.port（8080），
  // 那条 ws 连接过不去 —— 表现就是“页面能开、改代码不热更新”。
  //
  // 2026-10-28 的处理是直接把 clientPort 写成 443，代价是本地 npm run dev
  // 也被改坏（HMR 去连 443，同样不生效）。这里改成按环境判断：
  //   - VITE_HMR_CLIENT_PORT 显式指定优先（其他反代环境用这个）；
  //   - Google Cloud Shell / GitHub Codespaces 自动识别（它们的变量是确定的）；
  //   - 其余情况不设 hmr，保持 Vite 默认（本地即 8080）。
  const cloudIDE = env.CLOUD_SHELL === 'true' || env.CODESPACES === 'true'
  const hmrClientPort = Number(env.VITE_HMR_CLIENT_PORT || (cloudIDE ? 443 : 0))

  return {
    plugins: [react()],
    server: {
      // 监听所有网卡：容器 / Cloud Shell 里需要从外部访问。本地等价于 0.0.0.0。
      // 只想本机可访问时：`npm run dev -- --host 127.0.0.1`。
      host: true,
      port: 8080,
      ...(hmrClientPort ? { hmr: { clientPort: hmrClientPort } } : {}),
    },
    resolve: {
      alias: {
        '@platform': path.resolve(__dirname, './src/platform'),
        '@shared': path.resolve(__dirname, './src/platform/shared'),
        'peerdrive-client': path.resolve(__dirname, '../packages/peerdrive-client/src/index.js'),
      },
    },
    build: {
      sourcemap: false,
      rollupOptions: {
        output: {
          manualChunks(id: string) {
            if (
              id.includes('node_modules/react/') ||
              id.includes('node_modules/react-dom/') ||
              id.includes('node_modules/react-router')
            ) {
              return 'vendor-react'
            }
            if (id.includes('node_modules/peerjs')) {
              return 'vendor-peerjs'
            }
          },
        },
      },
    },
  }
})
