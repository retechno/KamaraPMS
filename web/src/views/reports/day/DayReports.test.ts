import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import ReportsView from '../ReportsView.vue'
import CancellationsView from './CancellationsView.vue'
import CorrectionsView from './CorrectionsView.vue'
import DailyFlashView from './DailyFlashView.vue'
import DepositLedgerView from './DepositLedgerView.vue'
import GuestLedgerView from './GuestLedgerView.vue'
import ZReportsView from './ZReportsView.vue'

/** The reports of the day (docs/architecture/38-reports-night-audit-pack.md, PR 1): the daily flash, the cancellations, the two ledgers, the corrections and the Z report. */
let GET = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a) } }))
let openPdf = vi.fn()
vi.mock('@/utils/documents', () => ({ openPdf: (...a: unknown[]) => openPdf(...a) }))

const VIEW = ['report.view']

async function setup(component: unknown, route: string, permissions = VIEW) {
  const pinia = createPinia()
  setActivePinia(pinia)
  setLocale('en')
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.clock = { business_date: '2026-10-29' } as never
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  void router.push(route)
  await router.isReady()
  const w = mount(component as never, { global: { plugins: [pinia, router] }, attachTo: document.body })
  await flushPromises()
  return w
}

const line = (key: string, label: string, over: Record<string, unknown> = {}) => ({
  key, label, kind: 'MONEY', today: '0', mtd: '0', last_month: '0', last_year: '0', budget: null, variance: null, variance_percent: null, favourable: null, ytd: '0', ...over,
})

function flash(over: Record<string, unknown> = {}) {
  const rooms = line('cc:1', 'Room charge', { today: '96200000', mtd: '2500000000', ytd: '9000000000' })
  const food = line('cc:2', 'Food', { today: '32100000' })
  const spa = line('cc:3', 'Spa treatment', { today: '4000000' })
  const total = (key: string, today: string) => line(key, `Total ${key}`, { today })
  const block = (kind: string, title: string, rows: unknown[], t: string) => ({ kind, title, rows, total: total(`block:${kind}`, t) })
  return {
    business_date: '2026-10-28', status: 'CLOSED', interim: false, decimals: 0,
    ranges: {}, fiscal_year_start: '2026-01-01', has_budget: false,
    rooms: [line('rooms_total', 'Total rooms', { kind: 'COUNT', today: '100' })], sold_by_type: [], activity: [], covers: [],
    revenue: {
      groups: [
        { code: 'A', title: 'Rooms', blocks: [block('ROOMS', 'Rooms', [rooms], '96200000')], total: total('group:A', '96200000') },
        { code: 'B', title: 'Food and beverage', blocks: [block('OUTLET', 'Restoran', [food], '32100000'), block('BANQUET', 'Banquet', [], '0')], total: total('group:B', '32100000') },
        { code: 'C', title: 'Other', blocks: [block('OUTLET', 'Spa', [spa], '4000000'), block('UNMAPPED', 'Unmapped', [line('cc:9', 'Misc', { today: '10000' })], '10000')], total: total('group:C', '4010000') },
      ],
      total_net: line('total_net', 'Total revenue (net)', { today: '132310000' }), service: line('service', 'Service charge'), tax: line('tax', 'Tax'),
      gtr: line('gtr', 'GTR', { today: '168770800' }), journal: null,
      by_source: [line('source_hotel', 'Hotel', { today: '96200000' }), line('source_pos', 'POS', { today: '36100000' }), line('source_banquet', 'Banquet', { today: '0' }), line('source_other', 'Other')],
    },
    payments: { by_cashier: [], by_method: [line('method:CASH', 'Cash', { today: '1000' })], gtp: line('gtp', 'GTP', { today: '1000' }), refunds: [], refunds_total: line('refunds_total', 'Refunds'), to_city_ledger: line('to_city_ledger', 'City ledger') },
    control: { balanced: true, today: { gtr: '1', gtp: '1', refunds: '0', to_city_ledger: '0', expected_change: '0', actual_change: '0', difference: '0' }, mtd: { gtr: '1', gtp: '1', refunds: '0', to_city_ledger: '0', expected_change: '0', actual_change: '0', difference: '0' } },
    signatures: { prepared_by: 'Dewi', prepared_at: '2026-10-28T22:00:00Z' }, warnings: [],
    ...over,
  }
}

describe('the daily flash report', () => {
  beforeEach(() => {
    GET = vi.fn()
    openPdf = vi.fn()
  })

  it('groups the revenue in A, B and C, with the outlet as the title of a block and the unmapped rows marked', async () => {
    GET = vi.fn(async () => ({ data: flash() }))
    const w = await setup(DailyFlashView, '/reports/daily-flash')
    expect(w.find('[data-testid=flash-group-A]').exists()).toBe(true)
    expect(w.get('[data-testid=flash-group-B]').text()).toContain('Restoran')
    expect(w.get('[data-testid=flash-group-B]').text()).toContain('Banquet')
    expect(w.get('[data-testid=flash-group-C]').text()).toContain('Spa')
    expect(w.get('[data-testid=flash-block-C-UNMAPPED]').text()).toContain('No revenue account')
    expect(w.get('[data-testid=sum-net]').text()).toBe('132,310,000')
    expect(GET.mock.calls[0]![1].params.query).toEqual({})
  })

  it('shows the budget columns only when the property has a budget, and a variance with its sign', async () => {
    GET = vi.fn(async () => ({ data: flash() }))
    let w = await setup(DailyFlashView, '/reports/daily-flash')
    expect(w.get('[data-testid=flash-rooms]').text()).not.toContain('Budget MTD')
    const withBudget = flash({ has_budget: true, rooms: [line('rooms_sold', 'Rooms sold', { kind: 'COUNT', today: '74', budget: '1761.3', variance: '141.7', favourable: true })] })
    GET = vi.fn(async () => ({ data: withBudget }))
    w = await setup(DailyFlashView, '/reports/daily-flash')
    expect(w.get('[data-testid=flash-rooms]').text()).toContain('Budget MTD')
    expect(w.get('[data-testid=flash-row-rooms_sold]').text()).toContain('+141.7')
  })

  it('says so when the day is still open, and shows the control balance difference without blocking anything', async () => {
    GET = vi.fn(async () => ({ data: flash({ interim: true, control: null }) }))
    let w = await setup(DailyFlashView, '/reports/daily-flash')
    expect(w.find('[data-testid=flash-interim]').exists()).toBe(true)
    expect(w.find('[data-testid=flash-control]').exists()).toBe(false)

    const bad = flash()
    bad.control.balanced = false
    bad.control.today.difference = '-1500'
    GET = vi.fn(async () => ({ data: { ...bad, warnings: [{ code: 'CONTROL_DIFFERENCE', params: { today: '-1500', mtd: '-1500' } }] } }))
    w = await setup(DailyFlashView, '/reports/daily-flash')
    expect(w.get('[data-testid=control-badge]').text()).toBe('Differs')
    expect(w.get('[data-testid=control-difference]').text()).toContain('-1,500')
    expect(w.get('[data-testid=warn-CONTROL_DIFFERENCE]').text()).toContain('Nothing is blocked')
  })

  it('downloads the CSV and opens the PDF of the date on the screen', async () => {
    GET = vi.fn(async () => ({ data: flash() }))
    const w = await setup(DailyFlashView, '/reports/daily-flash')
    URL.createObjectURL = vi.fn(() => 'blob:x')
    URL.revokeObjectURL = vi.fn()
    await w.get('[data-testid=csv]').trigger('click')
    await flushPromises()
    const csv = GET.mock.calls.at(-1)![1]
    expect(csv.params.query).toMatchObject({ date: '2026-10-28', format: 'csv' })
    expect(csv.parseAs).toBe('text')
    await w.get('[data-testid=pdf]').trigger('click')
    expect(openPdf).toHaveBeenCalledWith('/api/v1/properties/7/reports/daily-flash.pdf?date=2026-10-28')
  })

  it('tells that a day that is not closed cannot be shown', async () => {
    GET = vi.fn(async () => { throw new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'DATE_NOT_CLOSED', detail: 'not closed' }) })
    const w = await setup(DailyFlashView, '/reports/daily-flash')
    expect(w.get('[data-testid=report-error]').text()).toContain('not closed')
    expect(w.find('[data-testid=flash-summary]').exists()).toBe(false)
    // the notice follows the language of the moment: in Indonesian it is the message of the language file, not the server's
    setLocale('id')
    await flushPromises()
    expect(w.get('[data-testid=report-error]').text()).toContain('audit malam')
  })

  it('does not ask the server without the permission to view reports', async () => {
    const w = await setup(DailyFlashView, '/reports/daily-flash', [])
    expect(GET).not.toHaveBeenCalled()
    expect(w.get('[data-testid=no-access]').text()).toContain('report.view')
  })
})

describe('the other reports of the day', () => {
  beforeEach(() => {
    GET = vi.fn()
    openPdf = vi.fn()
  })

  const event = (over: Record<string, unknown>) => ({
    business_date: '2026-10-28', event_at: '2026-10-28T14:40:00Z', user_name: 'Dewi', kind: 'NO_SHOW', reservation_id: 1, confirmation_number: 'RSV-1', reservation_room_id: 1, guest: 'Andi', company: '',
    source: 'Phone', market: '', room_type: 'Deluxe', rate_plan: 'RO', rooms: 1, arrival: '2026-10-28', departure: '2026-10-30', nights: 2, lead_days: 0, value_lost: '2600000', reason: 'Did not arrive',
    fee_net: null, fee_gross: null, fee_date: null, fee_reversed: false, deposit_at_event: '0', deposit_now: '0', reinstated: false, ...over,
  })

  it('lists the cancellations with the fee only when it was posted, and sends the filters', async () => {
    const data = {
      from: '2026-10-23', to: '2026-10-29', kind: 'ALL', fees_posted: '1300000', fee_count: 1, deposits_held: '5000000',
      events: [event({ confirmation_number: 'RSV-1', fee_net: '1181818', fee_gross: '1300000' }), event({ confirmation_number: 'RSV-2', kind: 'CANCELLED', reinstated: true })],
      by_kind: [{ key: 'NO_SHOW', events: 1, rooms: 1, nights: 2, value: '2600000' }, { key: 'CANCELLED', events: 1, rooms: 1, nights: 2, value: '2600000' }],
      by_reason: [{ key: 'Did not arrive', events: 2, rooms: 2, nights: 4, value: '5200000' }], by_source: [{ key: 'Phone', events: 2, rooms: 2, nights: 4, value: '5200000' }], by_company: [],
    }
    GET = vi.fn(async () => ({ data }))
    const w = await setup(CancellationsView, '/reports/cancellations')
    expect(GET.mock.calls[0]![1].params.query).toEqual({ from: '2026-10-23', to: '2026-10-29' })
    expect(w.get('[data-testid=cx-events]').text()).toBe('2')
    expect(w.get('[data-testid=cx-nights]').text()).toBe('4')
    expect(w.get('[data-testid=cx-fee-0]').text()).toBe('1,300,000')
    expect(w.get('[data-testid=cx-fee-1]').text()).toBe('not posted')
    expect(w.find('[data-testid=cx-reinstated]').exists()).toBe(true)
    await w.get('[data-testid=cx-kind]').setValue('NO_SHOW')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(GET.mock.calls.at(-1)![1].params.query).toMatchObject({ kind: 'NO_SHOW' })
  })

  it('shows the guest ledger of a day with the check against the ledger account', async () => {
    const data = {
      as_of: '2026-10-28', release_mode: 'EVERY_DAY', charges: '154725000', settlements: '45570000', deposits: '34500000', balance: '74655000', deposit_held: '12490000', reconciled: false,
      rows: [{ folio_id: 1, folio_number: 'F-10233', folio_type: 'EVENT', status: 'OPEN', reservation_id: null, confirmation_number: '', room: 'Ballroom', owner: 'Seminar Bank', arrival: null, departure: null, charges: '22500000', settlements: '0', deposits: '10000000', balance: '12500000', deposit_held: '0', age_days: 0, age_bucket: '0-1' }],
      aging: [{ bucket: '0-1', folios: 1, balance: '12500000' }],
      checks: [{ key: 'ACCOUNT_GUEST_LEDGER', title: 'Guest ledger account', account: '1210 Guest receivables', report: '87145000', other: '87000000', difference: '145000' }],
    }
    GET = vi.fn(async () => ({ data }))
    const w = await setup(GuestLedgerView, '/reports/guest-ledger')
    expect(w.get('[data-testid=gl-row-F-10233]').text()).toContain('Event')
    expect(w.get('[data-testid=gl-balance]').text()).toBe('74,655,000')
    expect(w.get('[data-testid=reconciled]').text()).toBe('Differs')
    expect(w.get('[data-testid=check-diff-ACCOUNT_GUEST_LEDGER]').text()).toBe('145,000')
    expect(w.get('[data-testid=gl-age-0-1]').text()).toContain('12,500,000')
    expect(w.find('[data-testid=to-deposit-ledger]').exists()).toBe(true)
  })

  it('shows the deposit ledger with the folios that are overdue or do not add up', async () => {
    const row = (over: Record<string, unknown>) => ({ folio_id: 1, folio_number: 'F-1', folio_type: 'GUEST', reservation_id: 1, confirmation_number: 'RSV-1', owner: 'Hendra', arrival: '2026-11-05', opening: '5000000', received: '0', applied: '0', refunded: '0', closing: '5000000', balanced: true, expected_use: '2026-11-05', overdue: false, ...over })
    const data = {
      as_of: '2026-10-28', release_mode: 'ON_CLOSE', opening: '22750000', received: '3000000', applied: '12510000', refunded: '750000', closing: '12490000', reconciled: true,
      rows: [row({ folio_number: 'F-1', overdue: true }), row({ folio_id: 2, folio_number: 'F-2', balanced: false })],
      checks: [{ key: 'MOVEMENT', title: 'Movement', report: '12490000', other: '12490000', difference: '0' }],
    }
    GET = vi.fn(async () => ({ data }))
    const w = await setup(DepositLedgerView, '/reports/deposit-ledger')
    expect(w.get('[data-testid=dl-closing]').text()).toBe('12,490,000')
    expect(w.find('[data-testid=dl-overdue-F-1]').exists()).toBe(true)
    expect(w.find('[data-testid=dl-unbalanced-F-2]').exists()).toBe(true)
    expect(w.get('[data-testid=dl-movement]').text()).toContain('22,750,000')
    expect(w.get('[data-testid=reconciled]').text()).toBe('Matches')
  })

  it('lists the corrections by kind, folio and POS, with the share of the revenue', async () => {
    const row = (kind: string, over: Record<string, unknown> = {}) => ({ business_date: '2026-10-28', at: '2026-10-28T09:14:00Z', kind, folio: '', reservation: '', bill: '', outlet: '', charge_code: 'MB', department: '', description: 'Minibar', original_item_id: null, original_date: null, net: '-250000', service: '-25000', tax: '-27500', gross: '-302500', reason: 'Wrong room', done_by: 'Dewi', approved_by: 'Rina', ...over })
    const data = {
      from: '2026-10-23', to: '2026-10-29', kind: 'ALL', revenue_taken_back: '1161000', net_revenue: '139480000', share_percent: '0.83',
      rows: [row('VOID', { folio: 'F-10231' }), row('COMP', { bill: 'B-3402', outlet: 'Bar', net: '0', gross: '0' }), row('POS_VOID', { bill: 'B-3410', outlet: 'Restoran' })],
      by_kind: [{ key: 'VOID', count: 1, net: '-250000', gross: '-302500' }], by_reason: [], by_user: [{ key: 'Dewi', count: 3, net: '-500000', gross: '-605000' }], by_outlet: [{ key: 'Bar', count: 1, net: '0', gross: '0' }],
    }
    GET = vi.fn(async () => ({ data }))
    const w = await setup(CorrectionsView, '/reports/corrections')
    expect(w.get('[data-testid=co-kind-0]').text()).toBe('Void')
    expect(w.get('[data-testid=co-kind-1]').text()).toBe('Complimentary')
    expect(w.get('[data-testid=co-kind-2]').text()).toBe('POS void')
    expect(w.get('[data-testid=co-row-1]').text()).toContain('B-3402')
    expect(w.get('[data-testid=co-portion]').text()).toBe('0.83%')
    expect(w.get('[data-testid=co-taken-back]').text()).toBe('1,161,000')
  })

  it('shows the Z report with the X shift marked and the check against the cashier report', async () => {
    const block = (kind: string, number: string, over: Record<string, unknown> = {}) => ({
      kind, shift_id: 1, number, drawer: 'FO-1', cashier: 'Dewi', opened_at: '2026-10-28T07:00:00Z', closed_at: kind === 'Z' ? '2026-10-28T15:05:00Z' : null, business_date_opened: '2026-10-28', business_date_closed: null,
      opening_float: '2000000', cash_payments: '6450000', cash_refunds: '0', cash_receipts: '0', voided_after_close: '0', pay_ins: '0', pay_outs: '-5150000', drops: '0', expected_cash: '3300000', counted_cash: kind === 'Z' ? '3300000' : null, over_short: kind === 'Z' ? '0' : null,
      variance_reason: '', closed_by: '', approved_by: '', handed_over_to: '', tenders: [{ method: 'CARD', kind: 'PAYMENT', count: 3, amount: '38400000' }], total_takings: '0', ...over,
    })
    const drawer = { drawer: '', shifts: 2, opening_float: '4000000', cash_payments: '7550000', cash_refunds: '0', cash_receipts: '0', voided_after_close: '0', pay_ins: '0', pay_outs: '0', drops: '0', expected_cash: '6400000', counted_cash: null, over_short: null, total_takings: '0' }
    const data = {
      date: '2026-10-28', blocks: [block('Z', 'S-0412'), block('X', 'S-0415', { shift_id: 2 })], drawers: [], property: drawer, cash_off_shift: '0', voided_after_close: '0', reconciled: false,
      takings: [{ method: 'CASH', on_shift: '16330000', off_shift: '0', total: '16330000', cashier_report: '16000000', difference: '330000' }],
    }
    GET = vi.fn(async () => ({ data }))
    const w = await setup(ZReportsView, '/reports/z-reports', ['report.view', 'cashier.shift_manage'])
    expect(w.get('[data-testid=z-kind-S-0412]').text()).toBe('Z')
    expect(w.get('[data-testid=z-kind-S-0415]').text()).toBe('X')
    expect(w.get('[data-testid=z-row-S-0415]').text()).toContain('still open')
    expect(w.find('[data-testid=z-open-note]').exists()).toBe(true)
    expect(w.get('[data-testid=reconciled]').text()).toBe('Differs')
    expect(w.get('[data-testid=z-taking-CASH]').text()).toContain('330,000')
  })

  it('needs the permission to manage the cashier shifts for the Z report, and says which', async () => {
    const w = await setup(ZReportsView, '/reports/z-reports', VIEW)
    expect(GET).not.toHaveBeenCalled()
    expect(w.get('[data-testid=no-access]').text()).toContain('cashier.shift_manage')
  })
})

describe('the Reports page', () => {
  beforeEach(() => {
    GET = vi.fn()
  })

  it('links to the reports of the day, and to the Z report only for who manages the cashier shifts', async () => {
    let w = await setup(ReportsView, '/reports', VIEW)
    for (const k of ['flash', 'cancellations', 'guestLedger', 'depositLedger', 'corrections']) expect(w.find(`[data-testid=day-report-${k}]`).exists()).toBe(true)
    expect(w.find('[data-testid=day-report-z]').exists()).toBe(false)
    w = await setup(ReportsView, '/reports', ['report.view', 'cashier.shift_manage'])
    expect(w.get('[data-testid=day-report-z]').attributes('href')).toBe('/reports/z-reports')
  })
})
