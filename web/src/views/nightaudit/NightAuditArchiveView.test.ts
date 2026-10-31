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

const summary = (date: string, status: string) => ({ business_date: date, status, format_version: 1, built_at: `${date}T17:00:00Z`, build_ms: 200, built_late: false, sections_ok: status === 'COMPLETE' ? 15 : 14, sections_failed: status === 'COMPLETE' ? 0 : 1 })
const manifest = (status: string) => ({
  business_date: '2026-09-29', status, format_version: 1, built_at: '2026-09-29T17:00:00Z', built_by: 'Ani', build_ms: 200, built_late: false,
  sections: [
    { section: 'SUMMARY', status: 'OK', format_version: 1, built_late: false, size_bytes: 500, rebuildable: true },
    { section: 'FLASH', status: status === 'COMPLETE' ? 'OK' : 'FAILED', format_version: 1, built_late: false, size_bytes: 0, rebuildable: true },
  ],
})

function mountView(permissions = ['nightaudit.pack_view', 'nightaudit.pack_manage']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn((url: string) => Promise.resolve({ data: url.endsWith('/packs') ? { items: [summary('2026-09-29', 'PARTIAL'), summary('2026-09-28', 'COMPLETE')], next_cursor: null } : manifest('PARTIAL') }))
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

  it('needs the permission', async () => {
    const w = mountView(['guest.read'])
    await flushPromises()
    expect(w.find('[data-testid=no-access]').exists()).toBe(true)
    expect(GET).not.toHaveBeenCalled()
  })
})
