import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { router } from '@/router'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

// A page of setup needs the permission that manages what it shows (docs/architecture/33-setup-read-permissions.md): the menu leaves it out, and an address typed or kept goes to the home page.
vi.mock('@/api/client', () => ({ api: { GET: vi.fn().mockResolvedValue({ data: { data: [] } }), POST: vi.fn() } }))
vi.mock('@/api/session', () => ({ refreshSession: vi.fn().mockResolvedValue(null), setAccessToken: vi.fn() }))

function signIn(permissions: string[], admin = false): void {
  setActivePinia(createPinia())
  const auth = useAuthStore()
  auth.me = { user: { id: 5, email: 'u@hotel.com', full_name: 'U', is_tenant_admin: admin }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  auth.status = 'authenticated'
  const property = usePropertyStore()
  property.properties = [{ id: 7 } as never]
  property.loaded = true
  property.currentId = 7
}

async function goTo(path: string): Promise<string> {
  await router.push('/').catch(() => undefined)
  await router.push(path).catch(() => undefined)
  return router.currentRoute.value.path
}

describe('the guard of the pages of setup', () => {
  beforeEach(() => {
    signIn([])
  })

  it('turns housekeeping and the reception back to the home page', async () => {
    signIn(['housekeeping.update', 'housekeeping.assign'])
    for (const path of ['/setup/room-types', '/setup/rooms', '/setup/taxes', '/setup/rate-plans', '/setup/approval-rules', '/setup/companies', '/setup/free-quotas']) {
      expect(await goTo(path), path).toBe('/')
    }
    signIn(['reservation.read', 'reservation.create', 'frontdesk.checkin', 'folio.read', 'payment.post', 'guest.read'])
    for (const path of ['/setup/room-types', '/setup/charge-codes', '/setup/rates', '/setup/yield-rules', '/setup/properties/7']) {
      expect(await goTo(path), path).toBe('/')
    }
  })

  it('lets in who manages the page, and nobody else for it', async () => {
    signIn(['room.manage'])
    expect(await goTo('/setup/room-types')).toBe('/setup/room-types')
    expect(await goTo('/setup/rates')).toBe('/')
    signIn(['rate.manage'])
    expect(await goTo('/setup/rates')).toBe('/setup/rates')
    signIn(['property.manage'])
    expect(await goTo('/setup/approval-rules')).toBe('/setup/approval-rules')
    expect(await goTo('/setup/properties/7')).toBe('/setup/properties/7')
  })

  it('lets in a tenant administrator', async () => {
    signIn([], true)
    expect(await goTo('/setup/charge-codes')).toBe('/setup/charge-codes')
  })

  it('leaves the pages of the corridor to everyone', async () => {
    signIn(['housekeeping.update'])
    expect(await goTo('/room-status')).toBe('/room-status')
    expect(await goTo('/housekeeping/my')).toBe('/housekeeping/my')
    expect(await goTo('/housekeeping')).toBe('/housekeeping')
  })
})
