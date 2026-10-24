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
    // codeSplitting: false (Vite 8 replacement for inlineDynamicImports): src/core.js lazy-loads
    // peerjs via import('peerjs'); without this the ES artifact is split into
    // peerdrive-media.es.js + a hashed bundler-*.js chunk. dist/ is ignored at repo root and the
    // artifacts are force-added, so that chunk would silently be left out of the checked-in /
    // published single-file ES artifact (#396).
    rollupOptions: { output: { exports: 'named', codeSplitting: false } },
  },
})