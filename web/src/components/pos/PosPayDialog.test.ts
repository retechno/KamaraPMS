import { DOMWrapper, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import PosPayDialog from './PosPayDialog.vue'

let GET = vi.fn()
let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a) } }))

const bill = (over: object = {}) => ({
  id: 10, outlet_id: 1, bill_number: 'R-0123', status: 'OPEN', lines: [], payments: [], room_charges: [],
  totals: { base: '0', discount: '0', net: '0', service: '0', tax: '0', sold: '142857', drafts: '0', rounding: '0', paid: '0', transferred: '0', balance: '142857', payable: '142857', cash_payable: '142900' },
  ...over,
})
const rooms = [
  { stay_id: 31, room: '305', guest: 'Budi Santoso', target_type: 'GUEST', target_number: 'FOL1', routed: false, open: true, reason: '', limit: '500000', used: '58730', remaining: '441270' },
  { stay_id: 32, room: '306', guest: 'Intan', target_type: 'GUEST', target_number: 'FOL2', routed: false, open: false, reason: 'NEVER_OPENED', limit: null, used: null, remaining: null },
]

const mounted: VueWrapper[] = []
const body = (sel: string) => new DOMWrapper(document.body.querySelector(sel) as Element)
const has = (sel: string) => document.body.querySelector(sel) !== null
const text = (sel: string) => document.body.querySelector(sel)?.textContent?.trim() ?? ''

function setup(permissions = ['pos.cashier', 'pos.charge_room', 'cityledger.transfer'], current = bill()) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'k@hotel.test', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.current = { id: 7, currency_decimals: 0 } as never
  GET = vi.fn(async (path: string) => {
    if (path.endsWith('/pos/outlets/{id}')) return { data: { id: 1, drawer: 'D1', allow_room_charge: true, allow_company_charge: true } }
    if (path.endsWith('/room-lookup')) return { data: { data: rooms } }
    if (path.endsWith('/companies')) return { data: { data: [{ id: 3, name: 'Acme', pos_billing_allowed: true }, { id: 4, name: 'Other', pos_billing_allowed: false }] } }
    return { data: null }
  })
  POST = vi.fn(async () => ({ data: { bill: { ...current, status: 'CLOSED' }, change: '7100', closed: true, open_drawer: true } }))
  const w = mount(PosPayDialog, { props: { bill: current as never }, global: { plugins: [pinia] }, attachTo: document.body })
  mounted.push(w)
  return w
}

beforeEach(async () => {
  await setLocale('en')
})
afterEach(() => {
  while (mounted.length) mounted.pop()!.unmount()
  document.body.innerHTML = ''
})

describe('the payment of a bill', () => {
  it('starts with cash for the whole amount, rounded as the outlet rounds cash', async () => {
    setup()
    await flushPromises()
    expect((document.body.querySelector('[data-testid=amount-CASH]') as HTMLInputElement).value).toBe('142,900')
    expect(text('[data-testid=pay-total]')).toContain('142,900')
    expect(text('[data-testid=pay-rounding]')).toContain('142,857')
    expect(text('[data-testid=pay-rest]')).toBe('0')
  })

  it('shows the change of the cash that was handed over', async () => {
    setup()
    await flushPromises()
    await body('[data-testid=tendered]').setValue('150000')
    expect(text('[data-testid=pay-change]')).toBe('7,100')
  })

  it('mixes cash and a card: the rounding goes away and the rest must reach zero', async () => {
    setup()
    await flushPromises()
    await body('[data-testid=amount-CASH]').setValue('100000')
    await body('[data-testid=add-CARD]').trigger('click')
    expect((document.body.querySelector('[data-testid=amount-CARD]') as HTMLInputElement).value).toBe('42,857')
    expect(text('[data-testid=pay-rest]')).toBe('0')
    expect(has('[data-testid=pay-rounding]')).toBe(false)
    await body('[data-testid=pay-dialog] form').trigger('submit')
    await flushPromises()
    const call = POST.mock.calls[0]!
    expect(call[1].params.header['Idempotency-Key']).toMatch(/\S{8}/)
    expect(call[1].body.payments).toEqual([{ method: 'CASH', amount: '100000' }, { method: 'CARD', amount: '42857' }])
  })

  it('looks a room up, shows its lock and limit, and charges the room', async () => {
    const w = setup()
    await flushPromises()
    await body('[data-testid=add-ROOM]').trigger('click')
    await body('[data-testid=room-query]').setValue('30')
    await body('[data-testid=room-find]').trigger('click')
    await flushPromises()
    expect(text('[data-testid=room-31]')).toContain('441,270 left')
    expect(text('[data-testid=room-32]')).toContain('Locked')
    await body('[data-testid=room-31]').trigger('click')
    expect(text('[data-testid=room-chosen]')).toContain('305')
    await body('[data-testid=remove-CASH]').trigger('click')
    await body('[data-testid=exact-ROOM]').trigger('click')
    await body('[data-testid=pay-dialog] form').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls[0]![1].body.payments).toEqual([{ method: 'ROOM', amount: '142857', stay_id: 31 }])
    expect(w.emitted('paid')).toHaveLength(1)
  })

  it('does not offer a room or a company to a cashier who may not', async () => {
    setup(['pos.cashier'])
    await flushPromises()
    expect(has('[data-testid=add-ROOM]')).toBe(false)
    expect(has('[data-testid=add-COMPANY]')).toBe(false)
    expect(has('[data-testid=add-QRIS]')).toBe(true)
  })

  it('offers only the companies that may be billed by the outlets', async () => {
    setup()
    await flushPromises()
    await body('[data-testid=add-COMPANY]').trigger('click')
    await flushPromises()
    const opts = [...document.body.querySelectorAll('[data-testid=company-select] option')].map((o) => o.textContent)
    expect(opts).toContain('Acme')
    expect(opts).not.toContain('Other')
  })

  it('says what the room can take when the limit is exceeded, and charges that and the rest another way', async () => {
    const w = setup()
    await flushPromises()
    await body('[data-testid=add-ROOM]').trigger('click')
    await body('[data-testid=room-query]').setValue('30')
    await body('[data-testid=room-find]').trigger('click')
    await flushPromises()
    await body('[data-testid=room-31]').trigger('click')
    await body('[data-testid=remove-CASH]').trigger('click')
    await body('[data-testid=exact-ROOM]').trigger('click')
    POST = vi.fn(async () => {
      throw new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'OUTLET_LIMIT_EXCEEDED', detail: 'above the limit', context: { remaining: '41270', limit: '100000', used: '58730' } })
    })
    await body('[data-testid=pay-dialog] form').trigger('submit')
    await flushPromises()
    expect(text('[data-testid=pay-refusal]')).toContain('41,270')
    await body('[data-testid=use-remaining]').trigger('click')
    expect((document.body.querySelector('[data-testid=amount-ROOM]') as HTMLInputElement).value).toBe('41,270')
    expect(has('[data-testid=amount-CASH]')).toBe(true)
    expect(w.emitted('paid')).toBeUndefined()
  })

  it('explains a locked room and keeps the dialog open', async () => {
    setup()
    await flushPromises()
    await body('[data-testid=add-ROOM]').trigger('click')
    await body('[data-testid=room-query]').setValue('30')
    await body('[data-testid=room-find]').trigger('click')
    await flushPromises()
    await body('[data-testid=room-32]').trigger('click')
    await body('[data-testid=remove-CASH]').trigger('click')
    await body('[data-testid=exact-ROOM]').trigger('click')
    POST = vi.fn(async () => {
      throw new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'OUTLET_FOLIO_LOCKED', detail: 'locked', context: { reason: 'NEVER_OPENED' } })
    })
    await body('[data-testid=pay-dialog] form').trigger('submit')
    await flushPromises()
    expect(text('[data-testid=pay-refusal]')).toContain('never opened')
    expect(has('[data-testid=pay-dialog]')).toBe(true)
  })

  it('takes a part when the rest is not paid', async () => {
    setup()
    await flushPromises()
    await body('[data-testid=amount-CASH]').setValue('50000')
    expect(text('[data-testid=pay-rest]')).toBe('92,857')
    expect(has('[data-testid=pay-part]')).toBe(true)
    expect(text('[data-testid=pay-submit]')).toBe('Take this part')
  })
})
