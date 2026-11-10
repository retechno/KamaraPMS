import { test as base, expect, type Page } from 'playwright/test'
import { ApiClient } from './api'
import { TENANT } from './env'
import { Factories } from './factories'
import { seedState, type SeedState, type UserKey } from './state'

interface Fixtures {
  seed: SeedState
  /** The API as the tenant administrator: for preparing data and checking results. */
  admin: ApiClient
  data: Factories
  /** The API as another desk (for data that must belong to that user, such as a cashier shift). */
  apiAs: (user: UserKey) => Promise<ApiClient>
  /** Signs the page in as a desk, with the property E2E chosen and the app in Indonesian, then opens `path`. */
  loginAs: (user: UserKey, path?: string) => Promise<void>
  /** Fails the test on an uncaught error in the page or a 5xx answer of the API (on by itself). */
  pageGuard: void
}

export const test = base.extend<Fixtures>({
  seed: async ({}, use) => { await use(seedState()) },
  admin: async ({ seed }, use) => { await use(await ApiClient.login(seed.users.admin.email, seed.users.admin.password)) },
  data: async ({ admin, seed }, use) => { await use(new Factories(admin, seed)) },
  apiAs: async ({ seed }, use) => {
    await use(async (user) => ApiClient.login(seed.users[user].email, seed.users[user].password))
  },
  loginAs: async ({ page, seed }, use) => {
    await use(async (user, path = '/') => {
      await signIn(page, seed, user)
      await page.goto(path)
      await expect(page.getByTestId('topbar')).toBeVisible()
      await settle(page)
    })
  },
  pageGuard: [async ({ page }, use, testInfo) => {
    const problems: string[] = []
    const inFlight = { refresh: 0 }
    refreshes.set(page, inFlight)
    const isRefresh = (url: string) => url.includes('/api/v1/auth/refresh')
    page.on('request', (r) => { if (isRefresh(r.url())) inFlight.refresh++ })
    page.on('requestfinished', (r) => { if (isRefresh(r.url())) inFlight.refresh-- })
    page.on('requestfailed', (r) => { if (isRefresh(r.url())) inFlight.refresh-- })
    page.on('pageerror', (e) => problems.push(`page error: ${e.message}`))
    page.on('response', (r) => {
      if (r.status() >= 500 && r.url().includes('/api/')) problems.push(`${r.status()} ${r.request().method()} ${new URL(r.url()).pathname}`)
    })
    await use()
    if (problems.length && testInfo.status === testInfo.expectedStatus) {
      throw new Error(`the page had errors:\n  ${problems.join('\n  ')}`)
    }
  }, { auto: true }],
})

const refreshes = new WeakMap<Page, { refresh: number }>()

/**
 * Waits until no session refresh is on its way before a test leaves a page. A full page load (page.goto) during a refresh aborts its answer;
 * the server now gives the old cookie a new session within the grace (internal/iam/service.go Refresh, AUTH-10), but a test that leaves a page
 * only once it is done is steadier and cheaper (docs/qa: finding of run 4).
 */
export async function settle(page: Page): Promise<void> {
  await page.waitForLoadState('networkidle', { timeout: 3_000 }).catch(() => { /* the shell polls: never idle for long */ })
  const state = refreshes.get(page)
  if (state) await expect.poll(() => state.refresh, { timeout: 10_000 }).toBe(0)
}

/**
 * Signs in through the API from inside the browser context, so the refresh cookie belongs to this test alone.
 * (Refresh tokens rotate with reuse detection: a storageState shared by parallel workers would revoke the sessions.)
 */
export async function signIn(page: Page, seed: SeedState, user: UserKey): Promise<void> {
  await page.context().addInitScript(([tenant, propertyId]) => {
    try {
      localStorage.setItem('kamarapms.lastTenantCode', String(tenant))
      localStorage.setItem('kamarapms.currentPropertyId', String(propertyId))
      localStorage.setItem('pms.locale', 'id')
    } catch { /* storage refused: the app falls back to its defaults */ }
  }, [TENANT, seed.propertyId] as const)
  const { email, password } = seed.users[user]
  const r = await page.request.post('/api/v1/auth/login', { data: { tenant_code: TENANT, email, password } })
  expect(r.ok(), `login of ${user} (${email}): ${r.status()} ${await r.text()}`).toBeTruthy()
}

export { expect }
