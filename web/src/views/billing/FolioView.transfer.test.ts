import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import FolioView from './FolioView.vue'

let GET = vi.fn()
let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a), PATCH: vi.fn() } }))

const ref = (id: number, number: string, room = '') => ({ id, number, room })
const item = (over: object = {}) => ({
  id: 1, folio_id: 3, transaction_type: 'CHARGE', business_date: '2026-09-30', service_date: '2026-09-30', transaction_at: '2026-09-30T13:00:00Z', description: 'Minibar', charge_code: 'MINIBAR', charge_code_id: 2,
  quantity: '1', unit_price: '100000', price_mode: 'EXCLUSIVE', base_amount: '100000', discount_amount: '0', net_amount: '100000', rounding_adjustment: '0', service_charge_total: '0', tax_total: '0',
  debit: '100000', credit: '0', payment_id: null, reversed_by_item_id: null, group_code: 'A', components: [], revenue_account_code: null, transfer: null, ...over,
})
const items = [
  item({ id: 1 }),
  item({ id: 2 }),
  item({ id: 3, description: 'Laundry', charge_code: 'LAUNDRY', charge_code_id: 4, debit: '50000' }),
  item({ id: 4, description: 'Minibar moved', reversed_by_item_id: 5, transfer: { transfer_id: 1, role: 'ORIGINAL', kind: 'ITEM', is_return: false, hidden: true, from: ref(3, 'FOL000001'), to: ref(8, 'FOL000002'), origin: ref(3, 'FOL000001'), returned: false } }),
  item({ id: 5, transaction_type: 'REVERSAL', description: 'Reversal: Minibar moved', debit: '0', credit: '100000', transfer: { transfer_id: 1, role: 'OUT', kind: 'ITEM', is_return: false, hidden: true, from: ref(3, 'FOL000001'), to: ref(8, 'FOL000002'), origin: ref(3, 'FOL000001'), returned: false } }),
  item({ id: 6, description: 'Restaurant', charge_code: 'RESTAURANT', charge_code_id: 5, transfer: { transfer_id: 2, role: 'IN', kind: 'ITEM', is_return: false, hidden: false, from: ref(9, 'FOL000009', '102'), to: ref(3, 'FOL000001'), origin: ref(7, 'FOL000007'), returned: false } }),
  item({ id: 7, description: 'Transfer from FOL000009', charge_code: 'TRF', charge_code_id: 9, transfer: { transfer_id: 3, role: 'IN', kind: 'BALANCE', is_return: false, hidden: false, from: ref(9, 'FOL000009', '102'), to: ref(3, 'FOL000001'), origin: ref(9, 'FOL000009'), returned: false } }),
]
const folio = { id: 3, folio_number: 'FOL000001', folio_type: 'GUEST', status: 'OPEN', reservation_id: 9, stay_id: 4, opened_at: '', closed_at: null, version: 4, balance: '500000', totals: { debit: '600000', credit: '100000' }, items }
const codes = [
  { id: 2, code: 'MINIBAR', name: 'Minibar', charge_type: 'FOOD_BEVERAGE', is_active: true }, { id: 4, code: 'LAUNDRY', name: 'Laundry', charge_type: 'SERVICE', is_active: true },
  { id: 5, code: 'RESTAURANT', name: 'Restaurant', charge_type: 'FOOD_BEVERAGE', is_active: true }, { id: 9, code: 'TRF', name: 'Transfer balance', charge_type: 'TRANSFER', is_active: true },
]

const mounted: VueWrapper[] = []
function mountView(permissions = ['folio.read', 'folio.reverse', 'folio.post_charge']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'clerk@hotel.test', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.clock = { business_date: '2026-09-30' } as never
  GET = vi.fn(async (path: string) => {
    if (path.endsWith('/charge-codes')) return { data: { data: codes } }
    if (path.endsWith('/transfers')) return { data: { data: [] } }
    if (path.endsWith('/transfer-targets')) return { data: { data: [] } }
    if (path.endsWith('/folios')) return { data: { data: [] } }
    return { data: folio }
  })
  POST = vi.fn()
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  const w = mount(FolioView, { props: { id: '3' }, global: { plugins: [pinia, router] }, attachTo: document.body })
  mounted.push(w)
  return w
}

describe('FolioView: transfers between folios', () => {
  beforeEach(() => setLocale('en'))
  afterEach(() => {
    mounted.splice(0).forEach((w) => w.unmount())
    document.body.innerHTML = ''
  })

  it('ticks charges, not a reversed one, a correction or a line of the transfer code', async () => {
    const w = mountView()
    await flushPromises()
    for (const id of [1, 2, 3, 6]) expect(w.find(`[data-testid=select-${id}]`).exists()).toBe(true)
    for (const id of [4, 5, 7]) expect(w.find(`[data-testid=select-${id}]`).exists()).toBe(false)
    expect(w.get('[data-testid=transfer-items-open]').attributes('disabled')).toBeDefined()
    await w.get('[data-testid=select-1]').setValue(true)
    await w.get('[data-testid=select-3]').setValue(true)
    expect(w.get('[data-testid=selection-summary]').text()).toBe('2 transactions selected (150,000)')
    expect(w.get('[data-testid=transfer-items-open]').attributes('disabled')).toBeUndefined()
    await w.get('[data-testid=clear-selection]').trigger('click')
    expect(w.find('[data-testid=selection-summary]').exists()).toBe(false)
  })

  it('filters by charge code, selects every one of the code, and sends the code to the server', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=code-filter]').setValue('2')
    expect(w.find('[data-testid=item-1]').exists()).toBe(true)
    expect(w.find('[data-testid=item-3]').exists()).toBe(false)
    expect(w.get('[data-testid=select-visible]').text()).toBe('Select every one of this code')
    await w.get('[data-testid=select-visible]').trigger('click')
    expect(w.get('[data-testid=selection-summary]').text()).toBe('2 transactions selected (200,000)')
    await w.get('[data-testid=transfer-items-open]').trigger('click')
    await flushPromises()
    expect(document.body.querySelector('[data-testid=transfer-selection]')?.textContent).toContain('Every transaction of the code')
  })

  it('hides the corrections a transfer makes, with a toggle that brings them back', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.find('[data-testid=item-4]').exists()).toBe(false)
    expect(w.find('[data-testid=item-5]').exists()).toBe(false)
    expect(w.find('[data-testid=item-6]').exists()).toBe(true)
    await w.get('[data-testid=show-corrections]').setValue(true)
    expect(w.find('[data-testid=item-4]').exists()).toBe(true)
    expect(w.find('[data-testid=item-5]').exists()).toBe(true)
  })

  it('says where a line came from, with the folio as a link, and the transfer lines in words', async () => {
    const w = mountView()
    await flushPromises()
    const from = w.get('[data-testid=from-6]')
    expect(from.text()).toContain('from FOL000009')
    expect(from.text()).toContain('room 102')
    expect(from.text()).toContain('first from FOL000007')
    expect(w.get('[data-testid=from-link-6]').attributes('href')).toBe('/folios/9')
    expect(w.get('[data-testid=toggle-7]').text()).toBe('Transfer from FOL000009')
    expect(w.get('[data-testid=trf-7]').text()).toContain('room 102')
    setLocale('id')
    await flushPromises()
    expect(w.get('[data-testid=from-6]').text()).toContain('dari FOL000009')
    expect(w.get('[data-testid=from-6]').text()).toContain('kamar 102')
    setLocale('en')
  })

  it('shows what was transferred only when asked, and not to who may not reverse', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.find('[data-testid=transferred-list]').exists()).toBe(false)
    await w.get('[data-testid=show-transferred]').trigger('click')
    await flushPromises()
    expect(w.find('[data-testid=transferred-list]').exists()).toBe(true)
    expect(GET.mock.calls.some((c) => String(c[0]).endsWith('/folios/{id}/transfers'))).toBe(true)
    const reader = mountView(['folio.read'])
    await flushPromises()
    expect(reader.find('[data-testid=transfer-bar]').exists()).toBe(false)
    expect(reader.find('[data-testid=select-1]').exists()).toBe(false)
  })

  it('does not offer Reverse or Move on a line of TRF, nor Reverse on the copy a transfer made; the copy says where to return it', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.find('[data-testid=reverse-1]').exists()).toBe(true) // an ordinary charge of today
    expect(w.find('[data-testid=reverse-6]').exists()).toBe(false) // the copy of a transfer
    expect(w.find('[data-testid=reverse-7]').exists()).toBe(false) // a line of TRF
    expect(w.find('[data-testid=move-7]').exists()).toBe(false)
    // one note for the folio the lines came from, not one for each line
    expect(w.findAll('[data-testid^=copy-note-]')).toHaveLength(1)
    const note = w.get('[data-testid=copy-note-9]')
    expect(note.text()).toContain('1 line(s) moved here from FOL000009.')
    expect(note.text()).toContain('open FOL000009 → Show what was transferred → Return.')
    expect(note.get('a').attributes('href')).toBe('/folios/9')
    expect(w.get('[data-testid=item-7]').text()).toContain('Balance transfer') // the debit side of TRF is typed like its credit side
  })

  it('counts the group chips by the rows the table shows', async () => {
    const w = mountView()
    await flushPromises()
    // 7 lines, two of them the corrections of a transfer: hidden, so the chip says 5
    const rows = () => w.findAll('[data-testid^=item-]').filter((r) => /^item-\d+$/.test(r.attributes('data-testid') ?? ''))
    expect(w.get('[data-testid=group-ALL]').text()).toContain('5')
    expect(rows()).toHaveLength(5)
    await w.get('[data-testid=show-corrections]').setValue(true)
    expect(w.get('[data-testid=group-ALL]').text()).toContain('7')
    expect(w.get('[data-testid=group-A]').text()).toContain('7')
    expect(rows()).toHaveLength(7)
  })

  it('does not offer the transfer code among the charges that can be posted', async () => {
    const w = mountView()
    await flushPromises()
    const options = w.findAll('select[name=charge_code] option, [data-testid=charge-form] [role=option]').map((o) => o.text())
    expect(options.join(' ')).not.toContain('TRF')
  })
})
