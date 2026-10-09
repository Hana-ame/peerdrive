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
    },
  },
  build: { sourcemap: false },
})
