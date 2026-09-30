import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { getAccessToken, setAccessToken } from '@/api/session'
import { rememberedTenantCode, useAuthStore } from './auth'

let POST = vi.fn()
let GET = vi.fn()
vi.mock('@/api/client', () => ({ api: { POST: (...a: unknown[]) => POST(...a), GET: (...a: unknown[]) => GET(...a) } }))

let refreshResult: unknown = null
vi.mock('@/api/session', async (orig) => {
  const actual = await orig<typeof import('@/api/session')>()
  return { ...actual, refreshSession: vi.fn(async () => refreshResult) }
})

const me = {
  user: { id: 1, email: 'fd@hotel.com', full_name: 'Front Desk', is_tenant_admin: false, is_active: true, created_at: '', grants: [] },
  tenant: { id: 1, code: 'ABC', name: 'ABC Hotels' },
  properties: [{ id: 7, code: 'BALI', name: 'Hotel Bali', role: 'Front Desk', permissions: ['folio.read'] }],
}

describe('auth store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
    setAccessToken(null)
    refreshResult = null
    POST = vi.fn()
    GET = vi.fn(async () => ({ data: me }))
  })

  it('is anonymous when there is no session to restore', async () => {
    const auth = useAuthStore()
    await auth.init()
    expect(auth.status).toBe('anonymous')
    expect(GET).not.toHaveBeenCalled()
  })

  it('restores the session from the refresh cookie', async () => {
    refreshResult = { access_token: 'tok' }
    const auth = useAuthStore()
    await Promise.all([auth.init(), auth.init()]) // concurrent guards share one restore
    expect(auth.status).toBe('authenticated')
    expect(GET).toHaveBeenCalledTimes(1)
    expect(auth.displayName).toBe('Front Desk')
  })

  it('logs in, keeps the token in memory and remembers the tenant code', async () => {
    POST.mockResolvedValue({ data: { access_token: 'tok-2' } })
    const auth = useAuthStore()
    await auth.login('abc', 'fd@hotel.com', 'secret passphrase')
    expect(auth.isAuthenticated).toBe(true)
    expect(getAccessToken()).toBe('tok-2')
    expect(rememberedTenantCode()).toBe('ABC')
    expect(JSON.stringify(localStorage)).not.toContain('tok-2')
  })

  it('checks permissions per property', async () => {
    POST.mockResolvedValue({ data: { access_token: 't' } })
    const auth = useAuthStore()
    await auth.login('ABC', 'fd@hotel.com', 'x')
    expect(auth.can('folio.read', 7)).toBe(true)
    expect(auth.can('payment.post', 7)).toBe(false)
    expect(auth.can('folio.read', 8)).toBe(false)
  })

  it('signs out locally even if the server call fails', async () => {
    POST.mockResolvedValueOnce({ data: { access_token: 't' } }).mockRejectedValueOnce(new Error('offline'))
    const auth = useAuthStore()
    await auth.login('ABC', 'fd@hotel.com', 'x')
    await expect(auth.logout()).rejects.toThrow('offline')
    expect(auth.status).toBe('anonymous')
    expect(getAccessToken()).toBeNull()
  })
})
