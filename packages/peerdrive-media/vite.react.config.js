// vite.react.config.js — React 产物。
// 消费方（vite/webpack）import 'peerdrive-media' 时拿到预构建 ESM，
// 避免 node_modules 内 .jsx 无法被消费方 esbuild 处理的问题。
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  build: {
    lib: {
      entry: 'src/react/index.js',
      formats: ['es'],
      fileName: () => 'index.js',
    },
    rollupOptions: {
      // react/peerjs 由消费方提供（peerDependencies/dependencies）
      external: ['react', 'react-dom', 'react/jsx-runtime', 'peerjs'],
    },
    outDir: 'dist/react',
    emptyOutDir: true,
  },
})