import { defineConfig } from 'vitest/config'
import vue from '@vitejs/plugin-vue'
import { fileURLToPath, URL } from 'url'

export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  test: {
    environment: 'jsdom',
    environmentOptions: {
      jsdom: {
        url: 'http://localhost/',
      },
    },
    // Node 25+ ships a broken native globalThis.localStorage that shadows jsdom's.
    // Disable it so tests use jsdom's real Storage.
    execArgv: ['--no-webstorage'],
    include: ['src/**/*.{test,spec}.ts'],
  },
})
