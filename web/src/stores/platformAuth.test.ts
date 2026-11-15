import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { getPlatformToken, setPlatformToken } from '@/api/platformSession'
import { getAccessToken, setAccessToken } from '@/api/session'
import { usePlatformAuthStore } from './platformAuth'

let POST = vi.fn()
vi.mock('@/api/platformClient', () => ({ platformApi: { POST: (...a: unknown[]) => POST(...a) } }))

let refreshResult: unknown = null
vi.mock('@/api/platformSession', async (orig) => {
  const actual = await orig<typeof import('@/api/platformSession')>()
  return { ...actual, refreshPlatformSession: vi.fn(async () => refreshResult) }
})

const operator = { id: 1, email: 'op@example.test', full_name: 'Op One', is_active: true, totp_enrolled: true, last_login_at: null, recovery_codes_left: 10 }

describe('platform auth store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    setPlatformToken(null)
    setAccessToken('hotel-token')
    refreshResult = null
    POST = vi.fn()
  })

  it('is anonymous when there is no session to restore, and leaves the hotel session alone', async () => {
    const auth = usePlatformAuthStore()
    await auth.init()
    expect(auth.status).toBe('anonymous')
    expect(getAccessToken()).toBe('hotel-token')
  })

  it('restores the session from the platform refresh cookie, once for concurrent callers', async () => {
    refreshResult = { access_token: 'op-tok', operator }
    const auth = usePlatformAuthStore()
    await Promise.all([auth.init(), auth.init()])
    expect(auth.status).toBe('authenticated')
    expect(getPlatformToken()).toBe('op-tok')
    expect(auth.displayName).toBe('Op One')
    expect(getAccessToken()).toBe('hotel-token')
  })

  it('login answers the challenge and signs nobody in yet', async () => {
    POST = vi.fn(async () => ({ data: { challenge: 'c1', next: 'totp', expires_at: '' } }))
    const auth = usePlatformAuthStore()
    const ch = await auth.login('op@example.test', 'pw')
    expect(ch.next).toBe('totp')
    expect(POST).toHaveBeenCalledWith('/api/v1/platform/auth/login', { body: { email: 'op@example.test', password: 'pw' } })
    expect(auth.status).toBe('unknown')
  })

  it('verify opens the session with a code or with a recovery code', async () => {
    POST = vi.fn(async () => ({ data: { access_token: 'tok', operator } }))
    const auth = usePlatformAuthStore()
    await auth.verify('c1', { recoveryCode: 'ABCD-EFGH' })
    expect(POST).toHaveBeenCalledWith('/api/v1/platform/auth/totp', { body: { challenge: 'c1', code: undefined, recovery_code: 'ABCD-EFGH' } })
    expect(auth.isAuthenticated).toBe(true)
  })

  it('an enrolment hands the recovery codes to the caller and opens the console only when told', async () => {
    const codes = Array.from({ length: 10 }, (_, i) => `CODE-${i}`)
    POST = vi.fn(async () => ({ data: { access_token: 'tok', operator, recovery_codes: codes } }))
    const auth = usePlatformAuthStore()
    const done = await auth.enrollConfirm('c1', '123456')
    expect(done.codes).toEqual(codes)
    expect(auth.status).toBe('unknown') // the codes are still on the screen
    done.open()
    expect(auth.isAuthenticated).toBe(true)
    expect(JSON.stringify(auth.$state)).not.toContain('CODE-0') // the store never holds them
  })

  it('signing out clears the token even when the call fails', async () => {
    POST = vi.fn(async () => {
      throw new Error('offline')
    })
    refreshResult = { access_token: 'op-tok', operator }
    const auth = usePlatformAuthStore()
    await auth.init()
    await expect(auth.logout()).rejects.toThrow()
    expect(auth.status).toBe('anonymous')
    expect(getPlatformToken()).toBeNull()
  })
})
