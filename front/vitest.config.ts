import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'
import path from 'path'
import { fileURLToPath } from 'url'

const __dirname = path.dirname(fileURLToPath(import.meta.url))

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@platform': path.resolve(__dirname, './src/platform'),
      '@shared': path.resolve(__dirname, './src/platform/shared'),
      'peerdrive-client': path.resolve(__dirname, '../packages/peerdrive-client/src/index.js'),
    },
  },
  test: {
    environment: 'happy-dom',
    setupFiles: ['./tests/setup.js'],
  },
})
