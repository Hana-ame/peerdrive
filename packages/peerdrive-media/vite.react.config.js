// vite.react.config.js — React build artifact.
// When consumers (vite/webpack) import 'peerdrive-media', they get the pre-built ESM,
// avoiding issues where .jsx in node_modules cannot be processed by the consumer's esbuild.
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
      // react/peerjs are provided by consumers (peerDependencies/dependencies)
      external: ['react', 'react-dom', 'react/jsx-runtime', 'peerjs'],
    },
    outDir: 'dist/react',
    emptyOutDir: true,
  },
})