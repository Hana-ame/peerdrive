// vite.vanilla.config.js — vanilla 产物：peerjs 打进 bundle，<script> 直接可用。
import { defineConfig } from 'vite'

export default defineConfig({
  build: {
    lib: {
      entry: 'src/vanilla/main.js',
      formats: ['es', 'iife'],
      name: 'PeerMedia',
      fileName: (format) => `peerdrive-media.${format === 'iife' ? 'iife' : 'es'}.js`,
    },
    outDir: 'dist/vanilla',
    emptyOutDir: true,
    // named：IIFE 全局 PeerMedia 直接挂命名导出（PeerMedia.load/mount/client），
    // 避免消费者被迫写 PeerMedia.default.load（MIXED_EXPORTS 警告来源）。
    rollupOptions: { output: { exports: 'named' } },
  },
})