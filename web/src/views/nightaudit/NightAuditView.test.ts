import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { toastText } from '@/test/toasts'
import { ApiError } from '@/api/problem'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { setDisplayTimeZone } from '@/utils/format'
import NightAuditView from './NightAuditView.vue'

let GET = vi.fn()
let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a) } }))

const clean = () => ({
  business_date: '2026-09-30', property_local_time: '2026-09-30T20:30:00+07:00', time_guard_ok: true, night_audit_allowed_from: '2026-09-30T13:00:00Z', can_run: true,
  blockers: { unresolved_arrivals: [], unresolved_departures: [], charge_errors: [], invalid_charges: [] },
  missing_charges: { count: 1, items: [] }, tonight_charges: { count: 2, total: '2200000' },
  warnings: { stale_drafts: [], open_folios_of_cancelled_reservations: [], blocks_ending: [] },
})

const blocked = () => ({
  ...clean(), can_run: false,
  blockers: {
    unresolved_arrivals: [
      { reservation_room_id: 31, reservation_id: 9, confirmation_number: 'RES000009', guest: 'Siti', room_type: 'DLX', arrival_date: '2026-09-30' },
      { reservation_room_id: 32, reservation_id: 10, confirmation_number: 'RES000010', guest: 'Budi', room_type: 'STD', room: '201', arrival_date: '2026-09-30' },
    ],
    unresolved_departures: [{ stay_id: 5, stay_number: 'STY000005', guest: 'Ani', room: '101', departure_date: '2026-09-30' }],
    charge_errors: [{ stay_id: 6, stay_number: 'STY000006', service_date: '2026-09-30', reason: 'NO_NIGHTLY_RATE' }],
    invalid_charges: [{ stay_id: 7, stay_number: 'STY000007', service_date: '2026-10-05', folio_item_id: 70, reason: 'OUTSIDE_STAY' }],
  },
})

const result = {
  closed_business_date: '2026-09-30', new_business_date: '2026-10-01', room_charges_posted: 2,
  summary: {
    rooms: { total: 3, out_of_order: 0, out_of_service: 0, sellable: 3, occupied: 2, sold: 2 }, arrivals: 2, departures: 0, no_shows: 1,
    room_revenue: { net: '2000000', service: '0', tax: '0' }, revenue_by_charge_type: [], payments_by_method: [{ method: 'CASH', payments: '300000', refunds: '0', net: '300000' }],
    occupancy_percent: '66.67', adr: '1000000', revpar: '666667', room_charges_posted: 2,
  },
}

function mountView(preview: object = clean(), permissions = ['nightaudit.run', 'nightaudit.no_show']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.clock = { business_date: '2026-09-30' } as never
  property.refreshClock = vi.fn().mockResolvedValue(undefined)
  GET = vi.fn().mockResolvedValue({ data: preview })
  POST = vi.fn().mockResolvedValue({ data: result })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  return mount(NightAuditView, { global: { plugins: [pinia, router] } })
}

describe('NightAuditView', () => {
  beforeEach(() => {
    GET = vi.fn()
    POST = vi.fn()
  })

  afterEach(() => {
    setDisplayTimeZone(null)
    setLocale('en')
  })

  it('lists every blocker with a way to resolve it and will not run', async () => {
    const w = mountView(blocked())
    await flushPromises()
    expect(w.get('[data-testid=arrival-32]').text()).toContain('STD · 201')
    expect(w.get('[data-testid=arrival-31] a').attributes('href')).toBe('/reservations/9')
    expect(w.get('[data-testid=departure-5] a').attributes('href')).toBe('/stays/5')
    expect(w.get('[data-testid=charge-errors]').text()).toContain('NO_NIGHTLY_RATE')
    expect(w.get('[data-testid=invalid-charges]').text()).toContain('OUTSIDE_STAY')
    expect(w.find('[data-testid=cannot-run]').exists()).toBe(true)
    expect(w.get('[data-testid=run-audit]').attributes('disabled')).toBeDefined()
  })

  it('shows an open count as a blocker with the way to the counts, and the amenities of a frozen warehouse as a warning', async () => {
    const w = mountView({
      ...clean(), can_run: false,
      blockers: { ...clean().blockers, stock: [{ code: 'INVENTORY_COUNT_OPEN', ref: 'CNT-000001', message: 'the count CNT-000001 of Gudang Bar is still open: it freezes the warehouse', params: { number: 'CNT-000001', warehouse: 'Gudang Bar' } }] },
      warnings: { ...clean().warnings, stock: [{ code: 'AMENITY_HELD', ref: 'CNT-000001', message: 'the amenities warehouse is frozen by the count CNT-000001', params: { number: 'CNT-000001' } }] },
    })
    await flushPromises()
    expect(w.get('[data-testid=stock-blockers]').text()).toContain('CNT-000001')
    expect(w.get('[data-testid=stock-blockers] a').attributes('href')).toBe('/inventory/counts')
    expect(w.get('[data-testid=warnings] [data-testid=stock-AMENITY_HELD] a').attributes('href')).toBe('/inventory/amenities')
    expect(w.get('[data-testid=run-audit]').attributes('disabled')).toBeDefined()
  })

  it('shows why the day could not close with its journal, and will not run', async () => {
    const w = mountView({
      ...clean(), can_run: false,
      blockers: { ...clean().blockers, accounting_readiness: [{ code: 'ACCOUNT_MAP_MISSING', ref: 'CASH', message: 'the system account CASH is not mapped' }] },
    })
    await flushPromises()
    const box = w.get('[data-testid=accounting-readiness]')
    expect(box.text()).toContain('ACCOUNT_MAP_MISSING')
    expect(box.text()).toContain('the system account CASH is not mapped')
    expect(w.get('[data-testid=run-audit]').attributes('disabled')).toBeDefined()
  })

  it('marks exactly the selected arrivals as no-show, after a confirmation, and reloads', async () => {
    const w = mountView(blocked())
    await flushPromises()
    const button = () => w.get('[data-testid=mark-no-shows]')
    expect(button().attributes('disabled')).toBeDefined()
    await w.get('[data-testid=arrival-31] input').setValue(true)
    await w.get('input[name=reason]').setValue('did not come')
    expect(button().attributes('disabled')).toBeDefined() // not confirmed yet
    await w.get('input[name=confirm]').setValue(true)
    POST.mockResolvedValue({ data: { marked: [{ reservation_room_id: 31 }], remaining_blockers: {} } })
    await w.get('[data-testid=noshow-form]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls[0]).toEqual(['/api/v1/properties/{propertyId}/night-audit/no-shows', {
      params: { path: { propertyId: 7 } }, body: { business_date: '2026-09-30', reservation_room_ids: [31], confirm: true, reason: 'did not come' },
    }])
    expect(w.get('[data-testid=notice]').text()).toContain('1 arrival(s) marked')
    expect(GET.mock.calls.length).toBe(2) // the list was reloaded
  })

  it('selects every arrival at once and refreshes after a changed set', async () => {
    const w = mountView(blocked())
    await flushPromises()
    await w.get('[data-testid=select-all]').setValue(true)
    expect(w.get('[data-testid=mark-no-shows]').text()).toContain('Mark 2')
    await w.get('input[name=confirm]').setValue(true)
    POST.mockRejectedValue(new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'NO_SHOW_SET_CHANGED', detail: 'changed' }))
    await w.get('[data-testid=noshow-form]').trigger('submit')
    await flushPromises()
    expect(w.find('[data-testid=form-error]').exists()).toBe(false) // a toast, the list is refreshed
    expect(toastText()).toContain('changed')
    expect(GET.mock.calls.length).toBe(2)
  })

  it('runs the audit once confirmed and shows the closing summary and the new business date', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.get('[data-testid=charge-counts]').text()).toContain('1 missing night(s)')
    expect(w.get('[data-testid=run-audit]').attributes('disabled')).toBeDefined()
    await w.get('input[name=confirm_run]').setValue(true)
    await w.get('[data-testid=run-audit]').trigger('click')
    await flushPromises()
    expect(POST.mock.calls[0]).toEqual(['/api/v1/properties/{propertyId}/night-audit/run', { params: { path: { propertyId: 7 } }, body: { business_date: '2026-09-30' } }])
    expect(w.get('[data-testid=new-date]').text()).toBe('1 Oct 2026')
    expect(w.get('[data-testid=summary]').text()).toContain('66.67%')
    expect(usePropertyStore().refreshClock).toHaveBeenCalled()
  })

  it('shows the server refusal and refreshes the preview', async () => {
    const w = mountView()
    await flushPromises()
    POST.mockRejectedValue(new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'NIGHT_AUDIT_BLOCKED', detail: 'blocked' }))
    await w.get('input[name=confirm_run]').setValue(true)
    await w.get('[data-testid=run-audit]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid=form-error]').text()).toContain('NIGHT_AUDIT_BLOCKED')
    expect(w.find('[data-testid=result]').exists()).toBe(false)
    expect(GET.mock.calls.length).toBe(2)
  })

  it('warns of a cancelled reservation that still holds a deposit, with the folio to open and the list of all of them', async () => {
    const w = mountView({ ...clean(), warnings: { stale_drafts: [], open_folios_of_cancelled_reservations: [{ folio_id: 31, folio_number: 'FOL000031', confirmation_number: 'RES000007', balance: '-300000' }], blocks_ending: [] } })
    await flushPromises()
    const warnings = w.get('[data-testid=warnings]')
    expect(warnings.text()).toContain('FOL000031')
    expect(warnings.text()).toContain('of RES000007 is open with a balance of -300,000. The reservation is cancelled (a negative balance is a deposit that was kept).')
    expect(warnings.get('a[href="/folios/31"]').text()).toBe('FOL000031')
    expect(w.get('[data-testid=open-folios-all] a').attributes('href')).toBe('/folios?cancelled_with_balance=true')
    expect(w.find('[data-testid=cannot-run]').exists()).toBe(false) // a warning never stops the audit
  })

  it('explains the time guard and shows warnings; needs the permission', async () => {
    const early = mountView({ ...clean(), time_guard_ok: false, can_run: false, warnings: { stale_drafts: [{ reservation_room_id: 1, reservation_id: 2, confirmation_number: 'RES2', arrival_date: '2026-09-29' }], open_folios_of_cancelled_reservations: [], blocks_ending: [] } })
    await flushPromises()
    expect(early.get('[data-testid=too-early]').text()).toContain('cannot be closed before')
    expect(early.get('[data-testid=warnings]').text()).toContain('RES2')
    const denied = mountView(clean(), ['guest.read'])
    await flushPromises()
    expect(denied.find('[data-testid=no-access]').exists()).toBe(true)
    expect(GET).not.toHaveBeenCalled()
  })

  it('warns about the master folio of a group that has left, without blocking the audit', async () => {
    const w = mountView({ ...clean(), warnings: { ...clean().warnings, open_group_masters: [{ folio_id: 30, folio_number: 'FOL000030', group_code: 'GRP1', group_name: 'Conference', balance: '2000000' }] } })
    await flushPromises()
    const line = w.get('[data-testid=open-master-30]')
    expect(line.text()).toContain('Every room billed to the master folio of group GRP1 has left, and the master folio FOL000030 is still open (balance 2,000,000).')
    // a room that stays out of the master may still be in house: the sentence says which rooms have left
    expect(line.text()).not.toContain('The group GRP1 has left')
    expect(line.get('a').attributes('href')).toBe('/folios/30')
    expect(w.find('[data-testid=cannot-run]').exists()).toBe(false) // a warning, not a blocker
  })

  it('warns when the master folio a group routes to is closed, and still lets the audit run', async () => {
    const w = mountView({ ...clean(), warnings: { ...clean().warnings, group_masters_closed: [{ group_id: 4, group_code: 'GRP1', group_name: 'Conference', folio_number: 'FOL000030', stays: 3 }, { group_id: 5, group_code: 'GRP2', group_name: 'Wedding', folio_number: '', stays: 1 }] } })
    await flushPromises()
    expect(w.get('[data-testid=closed-master-4]').text()).toBe('Group GRP1: the master folio FOL000030 is closed, so the nights of 3 room(s) go to the guest folios until it is reopened.')
    expect(w.get('[data-testid=closed-master-5]').text()).toBe('Group GRP2 has instructions but no master folio: the nights of 1 room(s) go to the guest folios.')
    expect(w.find('[data-testid=cannot-run]').exists()).toBe(false)
  })

  it('says at the top whether the audit is ready and how many steps need attention', async () => {
    const ready = mountView()
    await flushPromises()
    expect(ready.get('[data-testid=readiness]').text()).toBe('Ready to run')
    expect(ready.get('[data-testid=audit-date]').text()).toBe('30 Sep 2026')
    const stuck = mountView(blocked())
    await flushPromises()
    expect(stuck.get('[data-testid=readiness]').text()).toBe('3 step(s) need attention') // arrivals, departures, room charges
    const early = mountView({ ...clean(), time_guard_ok: false, can_run: false })
    await flushPromises()
    expect(early.get('[data-testid=readiness]').text()).toBe('1 step(s) need attention')
  })

  it('marks each step done or in need of attention', async () => {
    const w = mountView(blocked())
    await flushPromises()
    expect(w.get('[data-testid=guard]').attributes('data-state')).toBe('ok')
    expect(w.get('[data-testid=arrivals]').attributes('data-state')).toBe('blocked')
    expect(w.get('[data-testid=departures]').attributes('data-state')).toBe('blocked')
    expect(w.get('[data-testid=charges]').attributes('data-state')).toBe('blocked')
    expect(w.get('[data-testid=run]').attributes('data-state')).toBe('blocked')
    const ok = mountView()
    await flushPromises()
    expect(ok.get('[data-testid=arrivals]').attributes('data-state')).toBe('ok')
    expect(ok.get('[data-testid=run]').attributes('data-state')).toBe('pending')
  })

  it('selects arrivals one by one and with the select-all box', async () => {
    const w = mountView(blocked())
    await flushPromises()
    const all = w.get('[data-testid=select-all]')
    expect((all.element as HTMLInputElement).checked).toBe(false)
    await w.get('[data-testid=arrival-31] input').setValue(true)
    await w.get('[data-testid=arrival-32] input').setValue(true)
    expect((w.get('[data-testid=select-all]').element as HTMLInputElement).checked).toBe(true)
    await w.get('[data-testid=select-all]').setValue(false)
    expect(w.get('[data-testid=mark-no-shows]').text()).toContain('Mark 0')
  })

  it('shows the closing summary as figures, and the readiness badge goes away once run', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('input[name=confirm_run]').setValue(true)
    await w.get('[data-testid=run-audit]').trigger('click')
    await flushPromises()
    const text = w.get('[data-testid=summary]').text()
    expect(text).toContain('2 of 3 (66.67%)')
    expect(text).toContain('2 / 0 / 1')
    expect(text).toContain('Payments Cash')
    expect(w.find('[data-testid=readiness]').exists()).toBe(false)
  })

  it('names the payments by the label of the method, not by its code', async () => {
    const methods = { ...result, summary: { ...result.summary, payments_by_method: ['CASH', 'CARD', 'BANK_TRANSFER', 'QRIS', 'NEW_METHOD'].map((method) => ({ method, payments: '1', refunds: '0', net: '1' })) } }
    for (const [lang, expected, notExpected] of [
      ['en', ['Payments Cash', 'Payments Card', 'Payments Bank transfer', 'Payments QRIS', 'Payments NEW_METHOD'], ['Payments CASH', 'Payments CARD', 'Payments BANK_TRANSFER']],
      ['id', ['Pembayaran Tunai', 'Pembayaran Kartu', 'Pembayaran Transfer bank', 'Pembayaran QRIS', 'Pembayaran NEW_METHOD'], ['Pembayaran CASH', 'Pembayaran CARD', 'Pembayaran BANK_TRANSFER']],
    ] as const) {
      setLocale(lang)
      const w = mountView()
      POST = vi.fn().mockResolvedValue({ data: methods })
      await flushPromises()
      await w.get('input[name=confirm_run]').setValue(true)
      await w.get('[data-testid=run-audit]').trigger('click')
      await flushPromises()
      const text = w.get('[data-testid=summary]').text()
      for (const e of expected) expect(text).toContain(e)
      for (const n of notExpected) expect(text).not.toContain(n)
      w.unmount()
    }
  })

  it('writes the figures of the closing summary with the separators of the language', async () => {
    const big = { ...result, summary: { ...result.summary, occupancy_percent: '4.00', room_revenue: { net: '1035000', service: '0', tax: '0' }, adr: '1035000', revpar: '41400', payments_by_method: [{ method: 'CASH', payments: '1500000', refunds: '0', net: '1500000' }] } }
    for (const [lang, money, percent] of [['en', '1,035,000', '4.00%'], ['id', '1.035.000', '4,00%']] as const) {
      setLocale(lang)
      const w = mountView()
      POST = vi.fn().mockResolvedValue({ data: big })
      await flushPromises()
      await w.get('input[name=confirm_run]').setValue(true)
      await w.get('[data-testid=run-audit]').trigger('click')
      await flushPromises()
      const text = w.get('[data-testid=summary]').text()
      expect(text).toContain(money)
      expect(text).toContain(percent)
      expect(text).toContain(lang === 'en' ? '(4.00%)' : '(4,00%)') // the hint under the occupancy
      expect(text).toContain(lang === 'en' ? '1,500,000' : '1.500.000') // a payment line
      expect(text).toContain(lang === 'en' ? '41,400' : '41.400') // RevPAR
      expect(text).not.toContain('1035000')
      expect(text).not.toContain('41400')
      w.unmount()
    }
  })

  it('shows the times of the time guard on the wall clock of the property, not as an ISO text', async () => {
    // Jayapura is UTC+9: neither the zone of the test machine nor the +07:00 offset that the server sent for the property time.
    setDisplayTimeZone('Asia/Jayapura')
    const early = { ...clean(), time_guard_ok: false, can_run: false, night_audit_allowed_from: '2026-09-30T13:00:00Z', property_local_time: '2026-09-30T20:30:00+07:00' }
    const en = mountView(early)
    await flushPromises()
    expect(en.get('[data-testid=too-early]').text()).toBe('The business date cannot be closed before 30 Sep 2026 22:00. Property time: 30 Sep 2026 22:30.')
    en.unmount()
    setLocale('id')
    const id = mountView(early)
    await flushPromises()
    const text = id.get('[data-testid=too-early]').text()
    expect(text).toBe('Tanggal bisnis belum bisa ditutup sebelum 30 Sep 2026 22:00. Waktu properti: 30 Sep 2026 22:30.')
    expect(text).not.toMatch(/\d{4}-\d{2}-\d{2}T/)
  })

  it('speaks Indonesian', async () => {
    setLocale('id')
    const w = mountView(blocked())
    await flushPromises()
    expect(w.get('[data-testid=readiness]').text()).toBe('3 langkah perlu ditangani')
    expect(w.get('[data-testid=mark-no-shows]').text()).toBe('Tandai 0 sebagai no-show')
    expect(w.get('[data-testid=cannot-run]').text()).toBe('Selesaikan hal-hal di atas terlebih dahulu.')
    setLocale('en')
  })

  it('shows the pack the run built, with the way to the archive, to who may read it; warns of an incomplete one', async () => {
    const manifest = { business_date: '2026-09-30', status: 'PARTIAL', format_version: 1, built_at: '2026-09-30T17:00:00Z', built_by: 'Ani', build_ms: 250, built_late: false, sections: [{ section: 'SUMMARY', status: 'OK' }, { section: 'FLASH', status: 'FAILED' }] }
    const w = mountView({ ...clean(), warnings: { ...clean().warnings, pack_incomplete: ['2026-09-28'] } }, ['nightaudit.run', 'nightaudit.pack_view'])
    GET.mockImplementation((url: string) => Promise.resolve({ data: url.endsWith('/packs/{date}') ? manifest : { ...clean(), warnings: { ...clean().warnings, pack_incomplete: ['2026-09-28'] } } }))
    await w.get('button[data-testid=refresh]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid=pack-incomplete-2026-09-28]').text()).toContain('28 Sep 2026')
    await w.get('input[name=confirm_run]').setValue(true)
    await w.get('[data-testid=run-audit]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid=pack-status]').text()).toBe('Partial')
    expect(w.get('[data-testid=pack]').text()).toContain('1 of 2 sections ready')
    expect(w.get('[data-testid=pack-archive]').attributes('href')).toBe('/night-audit/archive')
  })

  it('shows no pack to who may not read the archive', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('input[name=confirm_run]').setValue(true)
    await w.get('[data-testid=run-audit]').trigger('click')
    await flushPromises()
    expect(w.find('[data-testid=pack]').exists()).toBe(false)
  })
})
