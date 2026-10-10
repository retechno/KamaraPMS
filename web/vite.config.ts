import { fileURLToPath, URL } from 'node:url'
import tailwindcss from '@tailwindcss/vite'
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
    plugins: [vue(), tailwindcss()],
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
      setupFiles: ['src/test/setup.ts'],
      // One jsdom per worker is heavy. With a worker per thread (16 on the owner's laptop, next to Docker and PostgreSQL) the machine
      // runs out of memory and a test that takes 0.5 s alone took over 5 s and timed out: a different one in each run, always the heavy
      // first mounts (ReservationDetailView, FrontDeskView, FrontDeskLists). Six workers keep the slowest test under 3 s.
      maxWorkers: 6,
    },
  }
})
