import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import NightAuditRecipientsView from './NightAuditRecipientsView.vue'

let GET = vi.fn()
let POST = vi.fn()
let PATCH = vi.fn()
let DELETE = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a), PATCH: (...a: unknown[]) => PATCH(...a), DELETE: (...a: unknown[]) => DELETE(...a) } }))

const row = (id: number, email: string, sections: string[], is_active = true, name = '', last: [string, string] | null = null) => ({ id, email, name, sections, is_active, last_status: last?.[0] ?? null, last_pack_date: last?.[1] ?? null, created_at: '2026-09-01T00:00:00Z', updated_at: '2026-09-01T00:00:00Z' })

function mountView(permissions = ['nightaudit.pack_manage'], emailEnabled = true) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn().mockResolvedValue({ data: { email_enabled: emailEnabled, items: [
    row(1, 'gm@example.test', ['SUMMARY', 'FLASH'], true, 'General manager', ['SENT', '2026-09-28']),
    row(2, 'finance@example.test', ['SUMMARY', 'FLASH', 'GUEST_LEDGER', 'Z_REPORTS'], true, '', ['FAILED', '2026-09-26']),
    row(3, 'eng@example.test', ['OUT_OF_ORDER'], false),
  ] } })
  POST = vi.fn().mockResolvedValue({ data: row(9, 'new@example.test', ['SUMMARY', 'FLASH']) })
  PATCH = vi.fn().mockResolvedValue({ data: row(1, 'gm@example.test', ['SUMMARY']) })
  DELETE = vi.fn().mockResolvedValue({})
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  return mount(NightAuditRecipientsView, { attachTo: document.body, global: { plugins: [pinia, router] } })
}

describe('NightAuditRecipientsView', () => {
  beforeEach(() => {
    setLocale('en')
    document.body.innerHTML = ''
  })

  it('lists the recipients with their sections and the marks of the sensitive ones', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.get('[data-testid=email-state]').text()).toContain('E-mail is on')
    expect(w.get('[data-testid=recipient-1]').text()).toContain('Night audit summary, Daily flash report')
    expect(w.get('[data-testid=recipient-1]').text()).toContain('General manager')
    const finance = w.get('[data-testid=recipient-2]').text()
    expect(finance).toContain('4 sections')
    expect(finance).toContain('guest names')
    expect(finance).toContain('cash')
    expect(w.get('[data-testid=recipient-3]').text()).toContain('Inactive')
    expect(w.get('[data-testid=recipient-3]').text()).not.toContain('OUT_OF_ORDER')
  })

  it('shows the last message to each address, a failure marked', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.get('[data-testid=recipient-1]').text()).toContain('28 Sep 2026')
    expect(w.get('[data-testid=last-1]').text()).toBe('sent')
    expect(w.get('[data-testid=last-2]').text()).toBe('failed')
    expect(w.get('[data-testid=last-2]').classes().join(' ')).toContain('destructive')
    expect(w.get('[data-testid=recipient-3]').find('[data-testid=last-none]').text()).toBe('Nothing sent yet')
    w.unmount()
    setLocale('id')
    const id = mountView()
    await flushPromises()
    expect(id.get('[data-testid=last-1]').text()).toBe('terkirim')
    expect(id.get('[data-testid=last-2]').text()).toBe('gagal')
    expect(id.get('[data-testid=recipient-3]').text()).toContain('Belum ada pengiriman')
  })

  it('says when e-mail is off, and still keeps the list', async () => {
    const w = mountView(['nightaudit.pack_manage'], false)
    await flushPromises()
    expect(w.get('[data-testid=email-state]').text()).toContain('E-mail is off on this server')
    expect(w.find('[data-testid=recipient-1]').exists()).toBe(true)
  })

  it('adds a recipient who gets the summary and the flash unless told otherwise', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=add-recipient]').trigger('click')
    await flushPromises()
    const dialog = document.body.querySelector('[data-testid=recipient-dialog]') as HTMLElement
    expect(dialog).not.toBeNull()
    const boxes = (n: string) => dialog.querySelector(`input[name=section_${n}]`) as HTMLInputElement
    expect(boxes('SUMMARY').checked).toBe(true)
    expect(boxes('FLASH').checked).toBe(true)
    expect(boxes('GUEST_LEDGER').checked).toBe(false)
    expect(boxes('Z_REPORTS').checked).toBe(false)
    const email = dialog.querySelector('input[name=email]') as HTMLInputElement
    email.value = ' new@example.test '
    email.dispatchEvent(new Event('input'))
    boxes('REVENUE').click()
    await flushPromises()
    ;(document.body.querySelector('[data-testid=save-recipient]') as HTMLButtonElement).click()
    await flushPromises()
    expect(POST).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/night-audit/pack-recipients', {
      params: { path: { propertyId: 7 } }, body: { email: 'new@example.test', name: '', sections: ['SUMMARY', 'FLASH', 'REVENUE'], is_active: true },
    })
    expect(GET).toHaveBeenCalledTimes(2) // the list is read again
  })

  it('changes a recipient and removes one after asking', async () => {
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true)
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=edit-1]').trigger('click')
    await flushPromises()
    ;(document.body.querySelector('input[name=section_FLASH]') as HTMLInputElement).click()
    await flushPromises()
    ;(document.body.querySelector('[data-testid=save-recipient]') as HTMLButtonElement).click()
    await flushPromises()
    expect(PATCH).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/night-audit/pack-recipients/{id}', {
      params: { path: { propertyId: 7, id: 1 } }, body: { email: 'gm@example.test', name: 'General manager', sections: ['SUMMARY'], is_active: true },
    })
    await w.get('[data-testid=edit-2]').trigger('click')
    await flushPromises()
    ;(document.body.querySelector('[data-testid=remove-recipient]') as HTMLButtonElement).click()
    await flushPromises()
    expect(confirmSpy).toHaveBeenCalled()
    expect(DELETE).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/night-audit/pack-recipients/{id}', { params: { path: { propertyId: 7, id: 2 } } })
    confirmSpy.mockRestore()
  })

  it('shows the refusal of the server in the form', async () => {
    const w = mountView()
    await flushPromises()
    POST.mockRejectedValueOnce(new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'PACK_RECIPIENT_EXISTS', detail: 'this address is already a recipient of the property' }))
    await w.get('[data-testid=add-recipient]').trigger('click')
    await flushPromises()
    const email = document.body.querySelector('input[name=email]') as HTMLInputElement
    email.value = 'gm@example.test'
    email.dispatchEvent(new Event('input'))
    await flushPromises()
    ;(document.body.querySelector('[data-testid=save-recipient]') as HTMLButtonElement).click()
    await flushPromises()
    expect(document.body.textContent).toContain('this address is already a recipient of the property')
  })

  it('speaks Indonesian', async () => {
    setLocale('id')
    const w = mountView()
    await flushPromises()
    expect(w.text()).toContain('Penerima paket audit malam')
    expect(w.text()).toContain('E-mail aktif')
    expect(w.get('[data-testid=recipient-2]').text()).toContain('4 bagian')
    expect(w.get('[data-testid=recipient-2]').text()).toContain('nama tamu')
    expect(w.get('[data-testid=recipient-2]').text()).toContain('kas')
    expect(w.get('[data-testid=recipient-3]').text()).toContain('Nonaktif')
  })

  it('needs the permission to manage', async () => {
    const w = mountView(['nightaudit.pack_view'])
    await flushPromises()
    expect(w.find('[data-testid=no-access]').exists()).toBe(true)
    expect(w.find('[data-testid=add-recipient]').exists()).toBe(false)
    expect(GET).not.toHaveBeenCalled()
  })
})
