import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { router } from '@/router'
import { useAuthStore } from '@/stores/auth'
import { usePlatformAuthStore } from '@/stores/platformAuth'

// The routes of the console have meta.platform: the guard asks the console's store and never the hotel's (docs/architecture/39-platform-console.md 11).
vi.mock('@/api/client', () => ({ api: { GET: vi.fn().mockResolvedValue({ data: { data: [] } }), POST: vi.fn() } }))
vi.mock('@/api/platformClient', () => ({ platformApi: { GET: vi.fn(), POST: vi.fn() } }))

let hotelRefresh = vi.fn()
vi.mock('@/api/session', () => ({ refreshSession: (...a: unknown[]) => hotelRefresh(...a), setAccessToken: vi.fn() }))
let platformRefresh = vi.fn()
vi.mock('@/api/platformSession', async (orig) => {
  const actual = await orig<typeof import('@/api/platformSession')>()
  return { ...actual, refreshPlatformSession: (...a: unknown[]) => platformRefresh(...a) }
})

const operator = { id: 1, email: 'op@example.test', full_name: 'Op', is_active: true, totp_enrolled: true, last_login_at: null, recovery_codes_left: 10 }

async function goTo(path: string): Promise<string> {
  await router.push(path).catch(() => undefined)
  return decodeURIComponent(router.currentRoute.value.fullPath)
}

describe('the guard of the platform console', () => {
  beforeEach(async () => {
    // start on the sign-in page, then forget what the guard learned there: every test has its own stores and its own answers
    setActivePinia(createPinia())
    hotelRefresh = vi.fn().mockResolvedValue(null)
    platformRefresh = vi.fn().mockResolvedValue(null)
    await router.push('/platform/login').catch(() => undefined)
    setActivePinia(createPinia())
  })

  it('has meta.platform on every route under /platform, the login page also public, and puts them before the catch-all', () => {
    const routes = router.getRoutes().filter((r) => r.path.startsWith('/platform'))
    expect(routes.length).toBeGreaterThanOrEqual(8)
    expect(routes.every((r) => r.meta.platform === true)).toBe(true)
    expect(routes.filter((r) => r.meta.public).map((r) => r.path)).toEqual(['/platform/login'])
    const all = router.getRoutes().map((r) => r.path)
    expect(all.indexOf('/platform/tenants')).toBeLessThan(all.indexOf('/:pathMatch(.*)*'))
  })

  it('sends someone who is not signed in to /platform/login with the page they asked for, without calling the hotel refresh', async () => {
    expect(await goTo('/platform/tenants?status=grace')).toBe('/platform/login?redirect=/platform/tenants?status=grace')
    expect(platformRefresh).toHaveBeenCalled()
    expect(hotelRefresh).not.toHaveBeenCalled()
    expect(useAuthStore().status).toBe('unknown') // the hotel's store was never asked
  })

  it('lets a signed-in operator in, restoring the session once from the platform cookie', async () => {
    platformRefresh = vi.fn().mockResolvedValue({ access_token: 't', operator })
    expect(await goTo('/platform/plans')).toBe('/platform/plans')
    expect(await goTo('/platform/invoices')).toBe('/platform/invoices')
    expect(platformRefresh).toHaveBeenCalledTimes(1)
    expect(hotelRefresh).not.toHaveBeenCalled()
  })

  it('keeps a signed-in operator off the sign-in page, and sends a path that is not a page of the console to the tenants', async () => {
    platformRefresh = vi.fn().mockResolvedValue({ access_token: 't', operator })
    expect(await goTo('/platform/plans')).toBe('/platform/plans')
    expect(await goTo('/platform/login')).toBe('/platform/tenants')
    expect(await goTo('/platform/nothing/here')).toBe('/platform/tenants')
    expect(await goTo('/platform')).toBe('/platform/tenants')
  })

  it('does not let a signed-in hotel user into the console, and does not touch the console session for a hotel page', async () => {
    const hotel = useAuthStore()
    hotel.status = 'authenticated'
    expect(await goTo('/platform/tenants')).toContain('/platform/login')
    expect(usePlatformAuthStore().isAuthenticated).toBe(false)
    platformRefresh.mockClear()
    await router.push('/login').catch(() => undefined)
    expect(platformRefresh).not.toHaveBeenCalled()
  })
})
