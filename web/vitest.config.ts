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
    // Node 24's native --localstorage-file flag changes what globalThis.storage
    // looks like under jsdom; keep workers on the default flags so pool startup
    // cannot fail with ERR_WORKER_INVALID_EXEC_ARGV (seen with execArgv tweaks).
    include: ['src/**/*.{test,spec}.ts'],
  },
})
