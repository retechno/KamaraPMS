import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import NightAuditArchiveView from './NightAuditArchiveView.vue'

let GET = vi.fn()
let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a) } }))
const openPdf = vi.fn()
vi.mock('@/utils/documents', () => ({ openPdf: (...a: unknown[]) => openPdf(...a), downloadFile: vi.fn() }))

const summary = (date: string, status: string) => ({ business_date: date, status, format_version: 1, built_at: `${date}T17:00:00Z`, build_ms: 200, built_late: false, sections_ok: status === 'COMPLETE' ? 15 : 14, sections_failed: status === 'COMPLETE' ? 0 : 1, mail_sent: date === '2026-09-28' ? 3 : 0, mail_failed: date === '2026-09-29' ? 1 : 0, mail_queued: date === '2026-09-29' ? 2 : 0 })
const manifest = (status: string) => ({
  business_date: '2026-09-29', status, format_version: 1, built_at: '2026-09-29T17:00:00Z', built_by: 'Ani', build_ms: 200, built_late: false,
  sections: [
    { section: 'SUMMARY', status: 'OK', format_version: 1, built_late: false, size_bytes: 500, rebuildable: true },
    { section: 'FLASH', status: status === 'COMPLETE' ? 'OK' : 'FAILED', format_version: 1, built_late: false, size_bytes: 0, rebuildable: true },
  ],
})

const delivery = (over: object) => ({
  id: 1, email: 'gm@example.test', sections: ['SUMMARY', 'FLASH'], part: 1, parts: 1, status: 'SENT', attempts: 1, sent_at: '2026-09-29T17:05:00Z', queued_at: '2026-09-29T17:00:00Z', queued_by: 'Ani',
  pdf_sha256: '9f3a'.padEnd(60, '0') + 'c21e', renderer: '1', ...over,
})
const recipient = (id: number, email: string, is_active = true) => ({ id, email, name: '', sections: ['SUMMARY', 'FLASH'], is_active, created_at: '2026-09-01T00:00:00Z', updated_at: '2026-09-01T00:00:00Z' })

function mountView(permissions = ['nightaudit.pack_view', 'nightaudit.pack_manage'], emailEnabled = true) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn((url: string) => {
    if (url.endsWith('/deliveries')) {
      return Promise.resolve({ data: { email_enabled: emailEnabled, items: [
        delivery({ id: 3, email: 'fo@example.test', status: 'FAILED', attempts: 5, sent_at: null, pdf_sha256: undefined, renderer: undefined, last_error: '550 5.1.1 mailbox unavailable' }),
        delivery({ id: 2, email: 'big@example.test', status: 'FAILED', attempts: 1, sent_at: null, pdf_sha256: undefined, renderer: undefined, last_error: 'PACK_TOO_LARGE: the section X is 9 bytes' }),
        delivery({ id: 1, part: 2, parts: 3 }),
      ] } })
    }
    if (url.endsWith('/pack-recipients')) return Promise.resolve({ data: { email_enabled: emailEnabled, items: [recipient(11, 'gm@example.test'), recipient(12, 'fo@example.test'), recipient(13, 'off@example.test', false)] } })
    return Promise.resolve({ data: url.endsWith('/packs') ? { email_enabled: emailEnabled, items: [summary('2026-09-29', 'PARTIAL'), summary('2026-09-28', 'COMPLETE'), summary('2026-09-27', 'COMPLETE')], next_cursor: null } : manifest('PARTIAL') })
  })
  POST = vi.fn().mockResolvedValue({ data: manifest('COMPLETE') })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  return mount(NightAuditArchiveView, { global: { plugins: [pinia, router] } })
}

describe('NightAuditArchiveView', () => {
  beforeEach(() => {
    setLocale('en')
    openPdf.mockReset()
  })

  it('lists the packs and opens the sections of a day', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.get('[data-testid=pack-row-2026-09-29]').text()).toContain('14 ready, 1 failed')
    expect(w.get('[data-testid=pack-row-2026-09-28]').text()).toContain('Complete')
    await w.get('[data-testid=open-2026-09-29]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid=section-FLASH]').text()).toContain('Failed')
    expect(w.get('[data-testid=section-FLASH]').text()).toContain('Daily flash report')
    expect(w.get('[data-testid=section-SUMMARY]').text()).toContain('Night audit summary')
    expect(w.find('[data-testid=detail] th').exists()).toBe(true)
    expect(w.get('[data-testid=detail]').text()).not.toContain('Size')
    expect(w.find('[data-testid=csv-FLASH]').exists()).toBe(false) // nothing to download from a failed section
    expect(w.find('[data-testid=csv-SUMMARY]').exists()).toBe(false) // the summary has no table
    expect(w.find('[data-testid=json-SUMMARY]').exists()).toBe(true)
  })

  it('prints the whole pack or the chosen sections', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=open-2026-09-29]').trigger('click')
    await flushPromises()
    await w.get('[data-testid=pdf-all]').trigger('click')
    expect(openPdf).toHaveBeenLastCalledWith('/api/v1/properties/7/night-audit/packs/2026-09-29.pdf')
    await w.get('[data-testid=section-SUMMARY] input[type=checkbox]').setValue(true)
    await w.get('[data-testid=pdf-chosen]').trigger('click')
    expect(openPdf).toHaveBeenLastCalledWith('/api/v1/properties/7/night-audit/packs/2026-09-29.pdf?sections=SUMMARY')
  })

  it('builds a failed section again for who may manage, and not for who may only read', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=open-2026-09-29]').trigger('click')
    await flushPromises()
    await w.get('[data-testid=rebuild]').trigger('click')
    await flushPromises()
    expect(POST.mock.calls[0]?.[0]).toBe('/api/v1/properties/{propertyId}/night-audit/packs/{date}/rebuild')
    expect(w.get('[data-testid=notice]').text()).toContain('2026-09-29')
    const reader = mountView(['nightaudit.pack_view'])
    await flushPromises()
    await reader.get('[data-testid=open-2026-09-29]').trigger('click')
    await flushPromises()
    expect(reader.find('[data-testid=rebuild]').exists()).toBe(false)
    expect(reader.find('[data-testid=late]').exists()).toBe(false)
  })

  it('shows the e-mail of each day beside its pack', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.get('[data-testid=pack-row-2026-09-28]').text()).toContain('3 sent')
    const failed = w.get('[data-testid=pack-row-2026-09-29]').text()
    expect(failed).toContain('2 waiting')
    expect(failed).toContain('1 failed')
    expect(w.get('[data-testid=pack-row-2026-09-27]').text()).toContain('Not sent')
    const off = mountView(undefined, false)
    await flushPromises()
    expect(off.get('[data-testid=pack-row-2026-09-27]').text()).toContain('E-mail is off')
  })

  it('writes the e-mail columns in Indonesian', async () => {
    setLocale('id')
    const w = mountView()
    await flushPromises()
    expect(w.get('[data-testid=pack-row-2026-09-28]').text()).toContain('3 terkirim')
    expect(w.get('[data-testid=pack-row-2026-09-29]').text()).toContain('2 diantre')
    expect(w.get('[data-testid=pack-row-2026-09-27]').text()).toContain('Tidak dikirim')
  })

  it('shows the history of the messages with the hash and the renderer of what was sent', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=open-2026-09-29]').trigger('click')
    await flushPromises()
    const sent = w.get('[data-testid=delivery-1]')
    expect(sent.text()).toContain('gm@example.test')
    expect(sent.text()).toContain('2 sections')
    expect(sent.text()).toContain('part 2 of 3')
    expect(sent.text()).toContain('queued by Ani')
    expect(sent.get('[data-testid=delivery-hash]').text()).toContain('SHA-256 9f3a…c21e · renderer v1')
    expect(sent.text()).toContain('Sent')
    const failed = w.get('[data-testid=delivery-3]')
    expect(failed.text()).toContain('5 attempts')
    expect(failed.text()).toContain('550 5.1.1 mailbox unavailable')
    expect(failed.find('[data-testid=delivery-hash]').exists()).toBe(false)
    const big = w.get('[data-testid=delivery-2]').text()
    expect(big).toContain('One section is larger than a message can hold.')
    expect(big).not.toContain('PACK_TOO_LARGE')
  })

  it('sends the pack again to the recipients chosen, for who may resend', async () => {
    const w = mountView(['nightaudit.pack_view', 'nightaudit.pack_resend'])
    await flushPromises()
    await w.get('[data-testid=open-2026-09-29]').trigger('click')
    await flushPromises()
    const panel = w.get('[data-testid=resend]')
    expect(panel.text()).toContain('gm@example.test')
    expect(panel.text()).not.toContain('off@example.test') // an inactive recipient gets nothing
    expect(w.get('[data-testid=resend-send]').text()).toBe('Send again to 2 recipients')
    await w.get('input[name=resend_12]').setValue(false)
    expect(w.get('[data-testid=resend-send]').text()).toBe('Send again to 1 recipients')
    POST.mockResolvedValueOnce({ data: { queued: [{ email: 'gm@example.test', sections: ['SUMMARY'] }], skipped: [], sections_failed: [] } })
    await w.get('[data-testid=resend-send]').trigger('click')
    await flushPromises()
    expect(POST).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/night-audit/packs/{date}/resend', { params: { path: { propertyId: 7, date: '2026-09-29' } }, body: { recipient_ids: [11] } })
  })

  it('offers no resend to who may only read, and says so when e-mail is off', async () => {
    const reader = mountView(['nightaudit.pack_view'])
    await flushPromises()
    await reader.get('[data-testid=open-2026-09-29]').trigger('click')
    await flushPromises()
    expect(reader.find('[data-testid=resend]').exists()).toBe(false)
    expect(reader.find('[data-testid=delivery-1]').exists()).toBe(true)
    const off = mountView(['nightaudit.pack_view', 'nightaudit.pack_resend'], false)
    await flushPromises()
    await off.get('[data-testid=open-2026-09-29]').trigger('click')
    await flushPromises()
    expect(off.get('[data-testid=mail-off]').text()).toContain('E-mail is off on this server')
    expect(off.find('[data-testid=resend-send]').exists()).toBe(false)
  })

  it('needs the permission', async () => {
    const w = mountView(['guest.read'])
    await flushPromises()
    expect(w.find('[data-testid=no-access]').exists()).toBe(true)
    expect(GET).not.toHaveBeenCalled()
  })
})

describe('NightAuditArchiveView, what is seen is what is sent', () => {
  it('does not load the archive while a date of the filter is half typed, and says why', async () => {
    const w = mountView()
    await flushPromises()
    const before = GET.mock.calls.length
    await w.get('[data-testid=filters] input[data-slot=date-input-text]').setValue('3/2')
    await w.get('[data-testid=filters]').trigger('submit')
    await flushPromises()
    expect(GET.mock.calls.length).toBe(before)
    expect(w.text()).toContain('Write as dd/mm/yyyy')
  })

  it('does not build a pack for a date that is half typed, and says why', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=late] input[data-slot=date-input-text]').setValue('3/2')
    await w.get('[data-testid=late] form').trigger('submit')
    await flushPromises()
    expect(POST).not.toHaveBeenCalled()
    expect(w.get('[data-testid=late]').text()).toContain('Write as dd/mm/yyyy')
  })
})
