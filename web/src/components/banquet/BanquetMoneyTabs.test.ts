import { DOMWrapper, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { setLocale } from '@/i18n'
import { usePropertyStore } from '@/stores/property'
import BanquetFolioTab from './BanquetFolioTab.vue'
import BanquetLinesTab from './BanquetLinesTab.vue'

let GET = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: vi.fn(), PUT: vi.fn(), PATCH: vi.fn(), DELETE: vi.fn() } }))

const event = (over: Record<string, unknown> = {}) => ({ id: 20, number: 'BQ-000020', name: 'Lokakarya', status: 'DEFINITE', booking_group_id: null, sessions: [], ...over })
const estimate = (over: Record<string, unknown> = {}) => ({ lines: [], by_code: [], net: '0', service: '0', tax: '0', total: '0', ...over })
const money = (over: Record<string, unknown> = {}) => ({
  posting_mode: 'AUTO', posting_mode_own: false, estimate: estimate(), deposit_received: '0', schedule: [], schedule_total: '0', overdue: false,
  folio: { id: 5, number: 'FOL000005', status: 'OPEN', debit: '0', credit: '0', balance: '0' },
  policy: { base: '', tiers: [], current: null, days_left: 0 }, postings: [], cancelled_fee_item_id: null, groups: [], ...over,
})
const group = { group_id: 3, code: 'GRP1', name: 'Rombongan Maju', folio_id: 9, folio_number: 'FOL000009' }

const mounted: VueWrapper[] = []
async function mountTab(component: unknown, props: Record<string, unknown>) {
  const pinia = createPinia()
  setActivePinia(pinia)
  usePropertyStore().currentId = 7
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: { template: '<div />' } }, { path: '/folios/:id', component: { template: '<div />' } }] })
  await router.push('/')
  GET = vi.fn(async () => ({ data: { items: [] } }))
  const w = mount(component as never, { attachTo: document.body, props: props as never, global: { plugins: [pinia, router] } })
  mounted.push(w)
  await flushPromises()
  return w
}
const body = (sel: string) => new DOMWrapper(document.body.querySelector(sel) as Element)
const folioProps = (over: Record<string, unknown> = {}) => ({ propertyId: 7, event: event(), money: money(), businessDate: '2026-10-22', canManage: true, ...over })

afterEach(() => {
  mounted.splice(0).forEach((w) => w.unmount())
  document.body.innerHTML = ''
  setLocale('en')
})

describe('the transfer of the bill of an event to a group', () => {
  it('is open for an event that is not linked when a group has an open master folio, and links it in the dialog', async () => {
    const w = await mountTab(BanquetFolioTab, folioProps({ money: money({ groups: [group] }) }))
    const button = w.get('[data-testid=transfer-open]')
    expect(button.attributes('disabled')).toBeUndefined()
    await button.trigger('click')
    await flushPromises()
    expect(document.body.querySelector('[data-testid=transfer-dialog]')).not.toBeNull()
    expect(body('[data-testid=transfer-dialog]').text()).toContain('GRP1 · Rombongan Maju · FOL000009')
    expect(document.body.querySelector('[data-testid=transfer-link-note]')).not.toBeNull()
  })

  it('is off, and says why, when no group has an open master folio and the event is not linked', async () => {
    const w = await mountTab(BanquetFolioTab, folioProps())
    const button = w.get('[data-testid=transfer-open]')
    expect(button.attributes('disabled')).toBeDefined()
    expect(button.attributes('title')).toBe('No group has an open master folio: open the master folio of a group first.')
  })

  it('is off, and says why, for a person who cannot manage the banquet', async () => {
    const w = await mountTab(BanquetFolioTab, folioProps({ canManage: false, money: money({ groups: [group] }) }))
    const button = w.get('[data-testid=transfer-open]')
    expect(button.attributes('disabled')).toBeDefined()
    expect(button.attributes('title')).toBe('You need the permission to manage the banquet to transfer a bill.')
  })

  it('stays open for an event that is already linked even when no other group is listed', async () => {
    const w = await mountTab(BanquetFolioTab, folioProps({ event: event({ booking_group_id: 3 }) }))
    expect(w.get('[data-testid=transfer-open]').attributes('disabled')).toBeUndefined()
  })
})

describe('the estimate of an event', () => {
  it('shows the revenue by the number and the name of the account of the ledger, one row for the codes that share an account', async () => {
    const by_code = [
      { charge_code_id: 1, charge_code: 'BQT_FB', gl_account_code: '4240', gl_account_name: 'Pendapatan F&B banquet', net: '27686441' },
      { charge_code_id: 2, charge_code: 'BQT_EQUIPMENT', gl_account_code: '4410', gl_account_name: 'Sewa ruang rapat', net: '2461017' },
      { charge_code_id: 3, charge_code: 'BQT_ROOM', gl_account_code: '4410', gl_account_name: 'Sewa ruang rapat', net: '3691542' },
    ]
    const w = await mountTab(BanquetLinesTab, { propertyId: 7, event: event({ sessions: [{ id: 1, name: 'Fullday', session_date: '2026-12-01', status: 'ACTIVE', room_name: 'Saraswati A' }] }), lines: { lines: [], estimate: estimate({ by_code, net: '33839000' }) }, money: money(), items: [], packages: [], defaultMode: 'EXCLUSIVE', canManage: true })
    const list = w.get('[data-testid=estimate-by-code]')
    expect(list.findAll('li')).toHaveLength(2)
    expect(list.text()).toContain('4240 Pendapatan F&B banquet')
    expect(list.text()).toContain('4410 Sewa ruang rapat')
    expect(list.text()).not.toContain('BQT_FB')
  })
})
