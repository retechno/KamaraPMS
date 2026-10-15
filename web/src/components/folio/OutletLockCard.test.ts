import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import OutletLockCard from './OutletLockCard.vue'

let GET = vi.fn()
let PUT = vi.fn()
let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), PUT: (...a: unknown[]) => PUT(...a), POST: (...a: unknown[]) => POST(...a) } }))

const outlets = [
  { id: 1, code: 'RESTO', name: 'Restoran Teratai', is_active: true },
  { id: 2, code: 'BAR', name: 'Bar Kolam', is_active: true },
  { id: 3, code: 'SPA', name: 'Spa', is_active: true },
]
const lockedLock = { folio_id: 31, folio_number: 'FOL000031', folio_type: 'GUEST', folio_status: 'OPEN', status: 'LOCKED', reason: 'NEVER_OPENED', can_open: true, why_not: null,
  limit_amount: null, used: null, remaining: null, valid_mode: null, valid_on: null, period_no: 0, outlets: [], version: 0, folio_version: 3, business_date: '2026-10-15' }
const openLock = { ...lockedLock, status: 'OPEN', reason: null, limit_amount: '2000000.000', used: '253750.000', remaining: '1746250.000', valid_mode: 'UNTIL_CLOSE', period_no: 1, version: 2,
  outlets: [outlets[0], outlets[1]] }

const mounted: VueWrapper[] = []
function setup(permissions: string[] = ['folio.read', 'folio.outlet_charge_open'], lock: object = lockedLock) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'fo@hotel.test', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.current = { currency_decimals: 0 } as never
  GET = vi.fn(async (path: string) => (path.endsWith('/outlet-lock') ? { data: lock } : { data: { data: outlets } }))
  const w = mount(OutletLockCard, { props: { folioId: 31 }, global: { plugins: [pinia] } })
  mounted.push(w)
  return w
}
const type = async (w: VueWrapper, sel: string, value: string) => {
  const input = w.find(sel).element as HTMLInputElement
  input.value = value
  await w.find(sel).trigger('input')
}

beforeEach(() => {
  setLocale('en')
  PUT = vi.fn()
  POST = vi.fn()
})
afterEach(() => {
  while (mounted.length) mounted.pop()!.unmount()
})

describe('the lock of a folio for outlet charges', () => {
  it('says a folio that was never opened is locked and why, and offers to open it', async () => {
    const w = setup()
    await flushPromises()
    expect(w.get('[data-testid=lock-status]').text()).toBe('Locked')
    expect(w.get('[data-testid=lock-locked-note]').text()).toContain('never opened')
    // the form is there at once, with the button that opens the folio
    expect(w.find('[data-testid=outlet-lock-form]').exists()).toBe(true)
    expect(w.get('[data-testid=lock-save]').text()).toBe('Open for outlets')
  })

  it('does not offer to open it to who may only read folios', async () => {
    const w = setup(['folio.read'])
    await flushPromises()
    expect(w.get('[data-testid=lock-status]').text()).toBe('Locked')
    expect(w.find('[data-testid=outlet-lock-form]').exists()).toBe(false)
  })

  it('says why a folio cannot be opened at all', async () => {
    const w = setup(undefined, { ...lockedLock, can_open: false, why_not: 'STAY_NOT_IN_HOUSE' })
    await flushPromises()
    expect(w.get('[data-testid=lock-cannot-open]').text()).toContain('not in house')
    expect(w.find('[data-testid=outlet-lock-form]').exists()).toBe(false)
  })

  it('needs a limit and an outlet, then opens the folio with them', async () => {
    const w = setup()
    await flushPromises()
    expect(w.get('[data-testid=lock-missing]').text()).toContain('limit')
    expect(w.get('[data-testid=lock-save]').attributes('disabled')).toBeDefined()
    await type(w, 'input[name=outlet_limit]', '2000000')
    expect(w.get('[data-testid=lock-missing]').text()).toContain('outlet')
    await w.get('[data-testid=lock-outlet-RESTO]').setValue(true)
    await w.get('[data-testid=lock-outlet-BAR]').setValue(true)
    await w.get('[data-testid=lock-mode-today]').setValue(true)
    expect(w.get('[data-testid=lock-save]').attributes('disabled')).toBeUndefined()
    PUT.mockResolvedValue({ data: openLock })
    await w.get('form').trigger('submit')
    await flushPromises()
    const [path, opts] = PUT.mock.calls[0] as [string, { params: { path: { id: number } }; body: Record<string, unknown> }]
    expect(path).toBe('/api/v1/properties/{propertyId}/folios/{id}/outlet-lock')
    expect(opts.params.path.id).toBe(31)
    expect(opts.body).toEqual({ limit_amount: '2000000', valid_mode: 'TODAY', outlet_ids: [1, 2], version: undefined })
    expect(w.get('[data-testid=lock-status]').text()).toBe('Open')
    expect(w.get('[data-testid=lock-used]').text()).toBe('253,750')
    expect(w.get('[data-testid=lock-remaining]').text()).toBe('1,746,250')
    expect(w.emitted('changed')).toHaveLength(1)
  })

  it('shows the limit, what is used and what remains, and changes the lock with its version', async () => {
    const w = setup(undefined, openLock)
    await flushPromises()
    expect(w.get('[data-testid=lock-limit-shown]').text()).toBe('2,000,000')
    expect(w.text()).toContain('Restoran Teratai, Bar Kolam')
    expect(w.text()).toContain('Check-out')
    await w.get('[data-testid=lock-change-button]').trigger('click')
    expect((w.get('input[name=outlet_limit]').element as HTMLInputElement).value).toBe('2,000,000')
    await type(w, 'input[name=outlet_limit]', '3000000')
    PUT.mockResolvedValue({ data: { ...openLock, limit_amount: '3000000.000', version: 3 } })
    await w.get('form').trigger('submit')
    await flushPromises()
    expect((PUT.mock.calls[0] as [string, { body: { version: number; limit_amount: string } }])[1].body).toMatchObject({ version: 2, limit_amount: '3000000' })
  })

  it('locks by hand', async () => {
    const w = setup(undefined, openLock)
    await flushPromises()
    POST.mockResolvedValue({ data: { ...lockedLock, reason: 'MANUAL', limit_amount: '2000000.000', version: 3 } })
    await w.get('[data-testid=lock-lock-button]').trigger('click')
    await flushPromises()
    expect(POST.mock.calls[0]?.[0]).toBe('/api/v1/properties/{propertyId}/folios/{id}/outlet-lock/lock')
    expect(w.get('[data-testid=lock-status]').text()).toBe('Locked')
    expect(w.get('[data-testid=lock-locked-note]').text()).toContain('locked by hand')
    expect(w.find('[data-testid=outlet-lock-form]').exists()).toBe(true)
  })

  it('shows the answer of the server when the lock cannot be opened or changed', async () => {
    const w = setup()
    await flushPromises()
    await type(w, 'input[name=outlet_limit]', '100')
    await w.get('[data-testid=lock-outlet-SPA]').setValue(true)
    PUT.mockRejectedValue(new ApiError({ type: 'about:blank', title: 'x', status: 409, code: 'OUTLET_LOCK_NOT_ALLOWED', detail: 'this folio cannot be opened' }))
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(w.get('[data-testid=lock-error]').text()).toContain('cannot be opened')
    expect(w.get('[data-testid=lock-status]').text()).toBe('Locked')
  })
})
