// vite.vanilla.config.js — vanilla artifact: peerjs bundled, directly usable via <script>.
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
    // named: the IIFE global PeerMedia directly exposes named exports (PeerMedia.load/mount/client),
    // avoiding consumers being forced to write PeerMedia.default.load (source of MIXED_EXPORTS warning).
    rollupOptions: { output: { exports: 'named' } },
  },
})