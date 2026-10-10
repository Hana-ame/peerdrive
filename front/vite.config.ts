import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import path from 'path'
import { fileURLToPath } from 'url'

const __dirname = path.dirname(fileURLToPath(import.meta.url))

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
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
        manualChunks: {
          // Group core React stack into a single cacheable chunk.
          // These are stable across deploys; only app code changes invalidate the cache.
          'vendor-react': ['react', 'react-dom', 'react-router-dom'],
          // peerjs is already dynamically imported (await import('peerjs')) in
          // PeerJSConnect.jsx, so Rollup produces a separate chunk for it.
          // Naming it here ensures consistent output naming and prevents it
          // from being accidentally inlined into vendor-react.
          'vendor-peerjs': ['peerjs'],
        },
      },
    },
  },
})
