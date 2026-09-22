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
    //
    // The setup file supplies the jsdom gaps reka-ui needs (ResizeObserver,
    // matchMedia) once, instead of a stub copy-pasted into every suite that opens
    // a menu — a copy each new file could forget, which surfaced as an
    // intermittent unhandled rejection alongside an otherwise green run.
    setupFiles: ['./src/test-setup.ts'],
    include: ['src/**/*.{test,spec}.ts'],
  },
})
