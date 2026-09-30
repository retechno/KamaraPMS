import { fileURLToPath, URL } from 'node:url'
import vue from '@vitejs/plugin-vue'
import { loadEnv } from 'vite'
import { defineConfig } from 'vitest/config'

export default defineConfig(({ mode }) => {
  // Read PMS_* settings from the repository root .env (shared with the Go binaries).
  const env = loadEnv(mode, fileURLToPath(new URL('..', import.meta.url)), 'PMS_')

  // In development the Go API listens on 127.0.0.1:18080 (PMS_HTTP_ADDR in .env.example; 8080 is
  // often taken by other local services). The dev server proxies to it so the browser talks to a
  // single origin, exactly like production behind a reverse proxy.
  const apiTarget = process.env.PMS_API_URL ?? env.PMS_API_URL ?? 'http://127.0.0.1:18080'

  return {
    plugins: [vue()],
    resolve: {
      alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
    },
    server: {
      port: 5173,
      proxy: {
        '/api': apiTarget,
        '/healthz': apiTarget,
        '/readyz': apiTarget,
      },
    },
    test: {
      environment: 'jsdom',
      include: ['src/**/*.test.ts'],
    },
  }
})
