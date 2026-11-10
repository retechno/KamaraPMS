import { fileURLToPath } from 'node:url'
import { defineConfig, devices } from 'playwright/test'
import { API_ORIGIN, WEB_ORIGIN, E2E_API_ADDR } from './e2e/support/env'

/**
 * End-to-end tests (docs/qa/e2e-test-cases.md). Run them with scripts/e2e.sh from the repository root: it resets the database pms_e2e,
 * creates the tenant and builds bin/e2e first. `npx playwright test` alone works on a database that scripts/e2e.sh prepared before (E2E_KEEP_DB=1 does the same).
 *
 * Projects, in this order:
 *   setup    e2e/setup/seed.setup.ts: the property E2E, rooms, rates, roles, users and the demo data of every module (idempotent)
 *   modules  e2e/modules/*.spec.ts: one file per module; every test makes its own data through the API, so they run in parallel
 *   final    e2e/final/*.spec.ts: the night audit, which closes the business day: the teardown of setup, so it runs after every module, also when one failed
 */
const root = fileURLToPath(new URL('..', import.meta.url))
const bin = process.env.E2E_BIN
const exe = process.env.E2E_EXE ?? (process.platform === 'win32' ? '.exe' : '')

export default defineConfig({
  testDir: './e2e',
  outputDir: './test-results',
  timeout: 60_000,
  expect: { timeout: 10_000 },
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  workers: process.env.CI ? 2 : 3,
  reporter: process.env.CI ? [['list'], ['html', { open: 'never' }], ['github']] : [['list'], ['html', { open: 'never' }]],
  use: {
    baseURL: WEB_ORIGIN,
    locale: 'id-ID',
    timezoneId: 'Asia/Makassar',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
    actionTimeout: 15_000,
    navigationTimeout: 30_000,
  },
  projects: [
    { name: 'setup', testMatch: /setup\/.*\.setup\.ts/, teardown: 'final' },
    { name: 'modules', testMatch: /modules\/.*\.spec\.ts/, dependencies: ['setup'], use: { ...devices['Desktop Chrome'], viewport: { width: 1440, height: 900 } } },
    { name: 'final', testMatch: /final\/.*\.spec\.ts/, fullyParallel: false, use: { ...devices['Desktop Chrome'], viewport: { width: 1440, height: 900 } } },
  ],
  webServer: [
    {
      // The API on its own port and its own database (scripts/e2e.sh exports PMS_DATABASE_URL=pms_e2e and the rest).
      command: bin ? `"${bin}/api${exe}"` : 'go run ./cmd/api',
      cwd: root,
      url: `${API_ORIGIN}/readyz`,
      reuseExistingServer: !process.env.CI,
      timeout: 180_000,
      stdout: 'ignore',
      stderr: 'pipe',
      env: {
        PMS_HTTP_ADDR: E2E_API_ADDR,
        PMS_DATABASE_URL: process.env.PMS_E2E_DATABASE_URL ?? 'postgres://pms:pms@localhost:55432/pms_e2e?sslmode=disable',
        PMS_RATE_LIMIT_PER_MINUTE: '0',
        PMS_ENV: 'development',
      },
    },
    {
      command: 'npx vite --port 5174 --strictPort',
      url: WEB_ORIGIN,
      reuseExistingServer: !process.env.CI,
      timeout: 120_000,
      stdout: 'ignore',
      env: { PMS_API_URL: API_ORIGIN },
    },
  ],
})
