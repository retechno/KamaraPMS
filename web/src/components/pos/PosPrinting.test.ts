import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { setLocale } from '@/i18n'
import PosDevicesPanel from './PosDevicesPanel.vue'
import PosPrintStatus from './PosPrintStatus.vue'

let GET = vi.fn()
let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a) } }))
const confirmAnswer = { value: true }
vi.mock('@/composables/useConfirm', () => ({ confirm: async () => confirmAnswer.value }))

const mounted: VueWrapper[] = []
beforeEach(async () => {
  confirmAnswer.value = true
  await setLocale('id')
  setActivePinia(createPinia())
})
afterEach(() => {
  while (mounted.length) mounted.pop()!.unmount()
  document.body.innerHTML = ''
})

const device = (id: number, over: object = {}) => ({
  id, name: 'Kasir Lobby', kind: 'AGENT', token_prefix: 'kpd_abcd', status: 'ACTIVE', last_seen_at: '2026-10-16T05:00:00Z', agent_version: '0.1.0', queue_depth: 0,
  created_at: '2026-10-16T04:00:00Z', rotated_at: null, revoked_at: null, printers: 2, online: true, ...over,
})

describe('the devices panel', () => {
  function panel(list: object) {
    GET = vi.fn(async () => ({ data: list }))
    POST = vi.fn(async (path: string) => {
      if (path.endsWith('/enrollments')) return { data: { code: 'K7QM-4TXD', expires_at: new Date(Date.now() + 15 * 60_000).toISOString(), expires_in_seconds: 900 } }
      if (path.endsWith('/rotate-token')) return { data: { device: device(1), token: 'kpd_NEWTOKENNEWTOKEN' } }
      return { data: device(1, { status: 'REVOKED', online: false }) }
    })
    const w = mount(PosDevicesPanel, { props: { propertyId: 7 }, global: { plugins: [createPinia()] }, attachTo: document.body })
    mounted.push(w)
    return w
  }

  it('says what each agent is: online, offline, revoked or never connected', async () => {
    const w = panel({
      data: [device(1), device(2, { name: 'Bar PC', online: false }), device(3, { name: 'Lama', status: 'REVOKED', online: false, revoked_at: '2026-10-10T00:00:00Z' }), device(4, { name: 'Baru', online: false, last_seen_at: null, agent_version: null })],
      pending_enrollments: 1, enrollment_expires_at: null,
    })
    await flushPromises()
    expect(w.get('[data-testid=device-1]').attributes('data-state')).toBe('online')
    expect(w.get('[data-testid=device-2]').attributes('data-state')).toBe('offline')
    expect(w.get('[data-testid=device-3]').attributes('data-state')).toBe('revoked')
    expect(w.get('[data-testid=device-4]').attributes('data-state')).toBe('never')
    expect(w.get('[data-testid=device-1]').text()).toContain('versi 0.1.0')
    expect(w.get('[data-testid=pending-codes]').text()).toBe('1 kode pendaftaran masih berlaku.')
    // a revoked device offers neither replace nor revoke
    expect(w.find('[data-testid=device-revoke-3]').exists()).toBe(false)
    expect(w.find('[data-testid=device-rotate-3]').exists()).toBe(false)
  })

  it('shows the enrolment code once with its command and a countdown', async () => {
    const w = panel({ data: [], pending_enrollments: 0, enrollment_expires_at: null })
    await flushPromises()
    expect(w.get('[data-testid=devices-empty]').text()).toContain('Belum ada perangkat')
    await w.get('[data-testid=enrollment-new]').trigger('click')
    await flushPromises()
    const dialog = document.body.querySelector('[data-testid=enrollment-dialog]')!
    expect(dialog.querySelector('[data-testid=enrollment-code]')!.textContent).toBe('K7QM-4TXD')
    expect(dialog.querySelector('[data-testid=enrollment-command]')!.textContent).toContain('pos-agent enroll -server')
    expect(dialog.querySelector('[data-testid=enrollment-command]')!.textContent).toContain('-code K7QM-4TXD')
    expect(dialog.querySelector('[data-testid=enrollment-countdown]')!.textContent).toMatch(/Berlaku 1[45]:\d\d lagi/)
    expect(POST).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/pos/devices/enrollments', { params: { path: { propertyId: 7 } } })
    // closing it forgets the code: it is shown once
    ;(dialog.querySelector('[data-testid=enrollment-close]') as HTMLElement).click()
    await flushPromises()
    expect(document.body.querySelector('[data-testid=enrollment-code]')).toBeNull()
  })

  it('revokes after a confirmation and shows a replaced token once', async () => {
    const w = panel({ data: [device(1)], pending_enrollments: 0, enrollment_expires_at: null })
    await flushPromises()
    confirmAnswer.value = false
    await w.get('[data-testid=device-revoke-1]').trigger('click')
    await flushPromises()
    expect(POST).not.toHaveBeenCalled()
    confirmAnswer.value = true
    await w.get('[data-testid=device-revoke-1]').trigger('click')
    await flushPromises()
    expect(POST).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/pos/devices/{id}/revoke', { params: { path: { propertyId: 7, id: 1 } } })
    await w.get('[data-testid=device-rotate-1]').trigger('click')
    await flushPromises()
    expect(document.body.querySelector('[data-testid=token-value]')!.textContent).toBe('kpd_NEWTOKENNEWTOKEN')
  })
})

describe('the printer status of the cashier', () => {
  const printer = (id: number, name: string, state: string, over: object = {}) => ({
    id, name, kind: 'RECEIPT', connection_info: '', paper_width_mm: 80, codepage: 'CP437', opens_drawer: true, is_active: true, state, device_id: 1, device_name: 'PC', ...over,
  })
  function status(printers: object[], failed: object[] = [], props: object = {}) {
    GET = vi.fn(async (path: string) => {
      if (path.endsWith('/pos/printers')) return { data: { data: printers } }
      if (path.endsWith('/pos/stations')) return { data: { data: [{ id: 1, code: 'K', name: 'Kitchen', printer_id: 2, kds_enabled: false, is_active: true, late_after_minutes: 15 }] } }
      if (path.endsWith('/failed-print-jobs')) return { data: { data: failed } }
      return { data: null }
    })
    POST = vi.fn(async () => ({ data: { id: 'n', copy_no: 2, created: true } }))
    const w = mount(PosPrintStatus, { props: { propertyId: 7, outletId: 3, receiptPrinterId: 1, orderMode: 'PRINT', canReprint: true, ...props }, attachTo: document.body })
    mounted.push(w)
    return w
  }

  it('is ready when every printer of the outlet is online', async () => {
    const w = status([printer(1, 'Struk', 'ONLINE'), printer(2, 'Dapur', 'ONLINE', { kind: 'KITCHEN' })])
    await flushPromises()
    expect(w.get('[data-testid=printer-chip]').attributes('data-level')).toBe('ok')
    expect(w.get('[data-testid=printer-chip]').text()).toBe('Printer siap')
  })

  it('says "Printer offline" with the name when an agent is silent or a printer has no paper', async () => {
    const w = status([printer(1, 'Struk', 'OFFLINE'), printer(2, 'Dapur', 'ONLINE', { kind: 'KITCHEN' })])
    await flushPromises()
    expect(w.get('[data-testid=printer-chip]').attributes('data-level')).toBe('bad')
    expect(w.get('[data-testid=printer-chip]').text()).toBe('Printer offline: Struk')
    expect(w.get('[data-testid=printer-chip-hint]').text()).toContain('browser')
    const p = status([printer(1, 'Struk', 'PAPER_OUT'), printer(2, 'Dapur', 'OFFLINE', { kind: 'KITCHEN' })])
    await flushPromises()
    // one chip for each printer, each with its own cause
    expect(p.findAll('[data-testid=printer-chip]').map((c) => c.text())).toEqual(['Kertas habis: Struk', 'Printer offline: Dapur'])
    const c = status([printer(1, 'Struk', 'COVER_OPEN'), printer(2, 'Dapur', 'ONLINE', { kind: 'KITCHEN' })])
    await flushPromises()
    expect(c.findAll('[data-testid=printer-chip]').map((x) => x.text())).toEqual(['Tutup printer terbuka: Struk'])
  })

  it('is not worried about printers of other outlets, and a printer with no agent is not "offline"', async () => {
    const w = status([printer(1, 'Struk', 'NO_DEVICE', { device_id: null }), printer(9, 'Bar', 'OFFLINE')], [], { orderMode: 'DIRECT' })
    await flushPromises()
    expect(w.get('[data-testid=printer-chip]').attributes('data-level')).toBe('unknown')
    expect(w.get('[data-testid=printer-chip]').text()).toBe('Printer belum terhubung ke agen: Struk')
  })

  it('shows nothing for an outlet that prints from the browser only', async () => {
    const w = status([printer(9, 'Bar', 'OFFLINE')], [], { receiptPrinterId: null, orderMode: 'DIRECT' })
    await flushPromises()
    expect(w.find('[data-testid=print-status]').exists()).toBe(false)
  })

  it('lists what failed for good and reprints it with a reason', async () => {
    const failed = [{ id: 'job-1', printer_id: 1, printer_name: 'Struk', kind: 'RECEIPT', source_type: 'BILL', source_id: 10, bill_id: 10, bill_number: 'R-0012', copy_no: 1, attempts: 3, error_code: 'PAPER_OUT', created_at: '2026-10-16T05:00:00Z' }]
    const w = status([printer(1, 'Struk', 'ONLINE')], failed)
    await flushPromises()
    expect(w.get('[data-testid=failed-jobs]').text()).toContain('1 pekerjaan cetak gagal')
    expect(w.get('[data-testid=failed-job-1]').text()).toContain('Struk · R-0012')
    expect(w.get('[data-testid=failed-job-1]').text()).toContain('Kertas habis')
    await w.get('[data-testid=reprint-job-1]').trigger('click')
    await flushPromises()
    const submit = document.body.querySelector('[data-testid=reprint-submit]') as HTMLButtonElement
    expect(submit.disabled).toBe(true) // a reason is required
    const input = document.body.querySelector('[data-testid=reprint-reason]') as HTMLInputElement
    input.value = 'kertas sudah diisi'
    input.dispatchEvent(new Event('input'))
    await flushPromises()
    expect((document.body.querySelector('[data-testid=reprint-submit]') as HTMLButtonElement).disabled).toBe(false)
    ;(document.body.querySelector('[data-testid=reprint-submit]') as HTMLElement).click()
    await flushPromises()
    expect(POST).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/pos/print-jobs/{id}/reprint', { params: { path: { propertyId: 7, id: 'job-1' } }, body: { reason: 'kertas sudah diisi' } })
    expect(w.emitted('reprinted')).toHaveLength(1)
  })

  it('does not list failed jobs for someone who cannot reprint them', async () => {
    const w = status([printer(1, 'Struk', 'ONLINE')], [], { canReprint: false })
    await flushPromises()
    expect(GET.mock.calls.some((c) => String(c[0]).endsWith('/failed-print-jobs'))).toBe(false)
    expect(w.find('[data-testid=failed-jobs]').exists()).toBe(false)
  })

  it('shows the answer of the server when a reprint is refused', async () => {
    const failed = [{ id: 'job-1', printer_id: 1, printer_name: 'Struk', kind: 'TICKET', source_type: 'TICKET', source_id: 1, copy_no: 1, attempts: 3, error_code: 'CONNECT_FAILED', created_at: '2026-10-16T05:00:00Z' }]
    const w = status([printer(1, 'Struk', 'ONLINE')], failed)
    await flushPromises()
    POST = vi.fn(async () => {
      throw new ApiError({ type: 'about:blank', title: 'x', status: 409, code: 'PRINT_DOCUMENT_PURGED', detail: 'removed' })
    })
    await w.get('[data-testid=reprint-job-1]').trigger('click')
    await flushPromises()
    const input = document.body.querySelector('[data-testid=reprint-reason]') as HTMLInputElement
    input.value = 'x'
    input.dispatchEvent(new Event('input'))
    await flushPromises()
    ;(document.body.querySelector('[data-testid=reprint-submit]') as HTMLElement).click()
    await flushPromises()
    expect(document.body.querySelector('[data-testid=reprint-dialog]')).not.toBeNull()
    expect(w.emitted('reprinted')).toBeUndefined()
  })
})
