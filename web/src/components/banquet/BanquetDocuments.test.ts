import { DOMWrapper, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { useSubscriptionStore } from '@/stores/subscription'
import BanquetBeoTab from './BanquetBeoTab.vue'
import BanquetBell from './BanquetBell.vue'
import BanquetDocsTab from './BanquetDocsTab.vue'
import BanquetRecipientsCard from './BanquetRecipientsCard.vue'
import BanquetTransferDialog from './BanquetTransferDialog.vue'

// The BEO, the documents and the bell of the banquet (docs/architecture/36-banquet.md 4.6, mockup 42).

let GET = vi.fn()
let POST = vi.fn()
let PUT = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a), PUT: (...a: unknown[]) => PUT(...a), PATCH: vi.fn(), DELETE: vi.fn() } }))
vi.mock('@/utils/documents', async (orig) => ({ ...(await orig<typeof import('@/utils/documents')>()), openPdf: vi.fn() }))

const event = (over: Record<string, unknown> = {}) => ({ id: 20, number: 'BQ-000020', name: 'Pernikahan', status: 'DEFINITE', booking_group_id: null, contact_email: 'klien@contoh.test', sessions: [], ...over })
const section = (name: string, over: Record<string, unknown> = {}) => ({
  section: name, body: '', department_id: 1, department_name: 'Dapur', head: 'Budi', deputy: 'Ani', no_department: false, state: 'UNREAD', read_revision: null, changed_since: null, read_by: [], ...over,
})
const beoSession = (over: Record<string, unknown> = {}) => ({
  id: 11, no: 1, name: 'Resepsi', date: '2026-11-07', start: '12:00:00', end: '16:00:00', ends_next_day: false, room: 'Ballroom', setup: 'ROUND_TABLE', pax_estimated: 100, pax_guaranteed: null, status: 'ACTIVE',
  sections: [section('GENERAL', { department_id: null, no_department: true }), section('KITCHEN', { body: 'Tanpa kacang', state: 'CHANGED', read_revision: 2, changed_since: 2 }), section('SECURITY', { department_id: null, no_department: true, department_name: null })],
  run_of_show: [], ...over,
})
const beo = (over: Record<string, unknown> = {}) => ({
  event_id: 20, number: 'BQ-000020', name: 'Pernikahan', status: 'DEFINITE', revision: 3, draft: false, sessions: [beoSession()],
  revisions: [{ revision_no: 3, note: 'Tamu bertambah', changes: [{ kind: 'session_field', session_no: 1, field: 'pax_estimated', old: '100', new: '120' }], notified: 2, created_by_name: 'Dewi', created_at: '2026-10-25T10:00:00Z' }], recipients: [], ...over,
})

const mounted: VueWrapper[] = []
function prepare() {
  const pinia = createPinia()
  setActivePinia(pinia)
  usePropertyStore().currentId = 7
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: { template: '<div />' } }, { path: '/banquet/:rest(.*)', component: { template: '<div />' } }] })
  return { pinia, router }
}
async function mountIt(component: unknown, props: Record<string, unknown>) {
  const { pinia, router } = prepare()
  await router.push('/')
  const w = mount(component as never, { attachTo: document.body, props: props as never, global: { plugins: [pinia, router] } })
  mounted.push(w)
  await flushPromises()
  return w
}
const body = (sel: string) => new DOMWrapper(document.body.querySelector(sel) as Element)

afterEach(() => {
  mounted.splice(0).forEach((w) => w.unmount())
  document.body.innerHTML = ''
  setLocale('en')
})

describe('the BEO tab', () => {
  it('shows, for each section, where it stands: changed since a revision, and a section with no department says nobody is told', async () => {
    GET = vi.fn(async () => ({ data: beo() }))
    setLocale('id')
    const w = await mountIt(BanquetBeoTab, { propertyId: 7, event: event(), canManage: true })
    expect(w.get('[data-testid=beo-revision]').text()).toContain('Revisi 3')
    expect(w.get('[data-testid=beo-state-11-KITCHEN]').text()).toBe('Berubah sejak revisi 2')
    expect(w.get('[data-testid=beo-nodept-11-SECURITY]').text()).toContain('tidak ada yang diberi tahu')
    expect((w.get('[data-testid=beo-text-11-KITCHEN]').element as HTMLTextAreaElement).value).toBe('Tanpa kacang')
    expect(w.get('[data-testid=revision-3]').text()).toContain('Tamu bertambah')
    expect(w.get('[data-testid=revision-3]').text()).toContain('perkiraan tamu berubah dari 100 menjadi 120')
  })

  it('names the set-up as the app does, one reader once with the last revision, the time as elsewhere, and a department without a head tells nobody', async () => {
    const reader = (user_name: string, revision: number) => ({ user_name, revision, at: '2026-10-25T10:00:00Z' })
    GET = vi.fn(async () => ({
      data: beo({
        sessions: [beoSession({
          setup: 'CLASSROOM',
          sections: [
            section('KITCHEN', { state: 'READ', read_revision: 5, read_by: [reader('Chef Wayan', 3), reader('Pak Made', 4), reader('Chef Wayan', 5), reader('Pak Made', 5), reader('Chef Wayan', 4)] }),
            section('HOUSEKEEPING', { department_name: 'Kamar', head: null, deputy: null }),
            section('SECURITY', { department_id: null, no_department: true, department_name: null }),
          ],
        })],
        revisions: [{ revision_no: 3, note: '', changes: [{ kind: 'session_field', session_no: 1, field: 'setup', old: 'CLASSROOM', new: 'THEATER' }], notified: 1, created_by_name: 'Dewi', created_at: '2026-10-25T10:00:00Z' }],
      }),
    }))
    setLocale('id')
    const w = await mountIt(BanquetBeoTab, { propertyId: 7, event: event(), canManage: true })
    const head = w.get('[data-testid=beo-session-11]').text()
    expect(head).toContain('Kelas')
    expect(head).not.toContain('CLASSROOM')
    const rev = w.get('[data-testid=revision-3]').text()
    expect(rev).toContain('Teater')
    expect(rev).not.toContain('THEATER')
    expect(rev).toMatch(/25 Okt 2026 \d\d:\d\d/)
    expect(rev).not.toContain('2026-10-25')
    expect(w.get('[data-testid=beo-session-11]').text()).toContain('Dibaca oleh Chef Wayan (r5), Pak Made (r5)')
    expect(w.get('[data-testid=beo-session-11]').text()).not.toContain('(r3)')
    expect(w.get('[data-testid=beo-nodept-11-HOUSEKEEPING]').text()).toContain('tidak ada yang diberi tahu saat berubah')
    expect(w.find('[data-testid=beo-nodept-11-KITCHEN]').exists()).toBe(false)
  })

  it('marks a section as read with the session and the section, and reloads the marks', async () => {
    GET = vi.fn(async () => ({ data: beo() }))
    POST = vi.fn(async () => ({ data: beo({ sessions: [beoSession({ sections: [section('KITCHEN', { body: 'Tanpa kacang', state: 'READ', read_revision: 3 })] })] }) }))
    const w = await mountIt(BanquetBeoTab, { propertyId: 7, event: event(), canManage: true })
    await w.get('[data-testid=beo-ack-11-KITCHEN]').trigger('click')
    await flushPromises()
    expect(POST).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/banquet/events/{id}/beo/ack', expect.objectContaining({ body: { session_id: 11, section: 'KITCHEN' } }))
    expect(w.find('[data-testid=beo-ack-11-KITCHEN]').exists()).toBe(false)
  })

  it('saves the texts of a session as one request and does not offer the save before something is typed', async () => {
    GET = vi.fn(async () => ({ data: beo() }))
    PUT = vi.fn(async () => ({ data: beo() }))
    const w = await mountIt(BanquetBeoTab, { propertyId: 7, event: event(), canManage: true })
    expect(w.get('[data-testid=beo-save-11]').attributes('disabled')).toBeDefined()
    await w.get('[data-testid=beo-text-11-KITCHEN]').setValue('Halal saja')
    await w.get('[data-testid=beo-text-11-KITCHEN]').trigger('input')
    expect(w.get('[data-testid=beo-save-11]').attributes('disabled')).toBeUndefined()
    await w.get('[data-testid=beo-save-11]').trigger('click')
    await flushPromises()
    expect(PUT).toHaveBeenCalledTimes(1)
    const call = PUT.mock.calls[0]![1] as { body: { sections: Record<string, string> } }
    expect(call.body.sections.KITCHEN).toBe('Halal saja')
  })

  it('is read-only for a person who cannot manage the banquet, and says that a draft has no revisions', async () => {
    GET = vi.fn(async () => ({ data: beo({ draft: true, revision: 0, revisions: [], sessions: [beoSession({ sections: [section('KITCHEN', { state: 'DRAFT' })] })] }) }))
    const w = await mountIt(BanquetBeoTab, { propertyId: 7, event: event({ status: 'TENTATIVE' }), canManage: false })
    expect(w.find('[data-testid=beo-text-11-KITCHEN]').exists()).toBe(false)
    expect(w.get('[data-testid=beo-revision]').text()).toContain('Draft')
    expect(w.find('[data-testid=beo-ack-11-KITCHEN]').exists()).toBe(false)
    expect(w.get('[data-testid=beo-history]').text()).toContain('No revisions yet.')
  })
})

describe('the documents tab', () => {
  const docs = { data: [{ id: 5, kind: 'CONTRACT', number: 'BQC-000001', revision_no: 3, created_at: '2026-10-25T10:00:00Z', created_by_name: 'Dewi' }] }
  const emails = (enabled: boolean) => ({ enabled, data: [{ id: 9, kind: 'BANQUET_CONTRACT', to: 'klien@contoh.test', status: 'FAILED', attempts: 5, last_error: 'mailbox full', sent_at: null, created_at: '2026-10-25T10:00:00Z', document_id: 5, document_number: 'BQC-000001', revision_no: 3, language: 'id' }] })

  it('lists the contracts and the e-mails with their state', async () => {
    GET = vi.fn(async (path: string) => ({ data: path.endsWith('/emails') ? emails(true) : docs }))
    const w = await mountIt(BanquetDocsTab, { propertyId: 7, event: event(), canManage: true, canConfirm: true })
    expect(w.get('[data-testid=contract-5]').text()).toContain('BQC-000001')
    expect(w.get('[data-testid=email-status-9]').text()).toBe('Failed')
    expect(w.get('[data-testid=email-9]').text()).toContain('mailbox full')
  })

  it('sends the contract to the address in the dialog, in the language chosen', async () => {
    GET = vi.fn(async (path: string) => ({ data: path.endsWith('/emails') ? emails(true) : docs }))
    POST = vi.fn(async () => ({ data: {} }))
    const w = await mountIt(BanquetDocsTab, { propertyId: 7, event: event(), canManage: true, canConfirm: true })
    await w.get('[data-testid=contract-send]').trigger('click')
    await flushPromises()
    expect((body('[data-testid=send-dialog] input[name=to]').element as HTMLInputElement).value).toBe('klien@contoh.test')
    await body('[data-testid=send-dialog] select[name=language]').setValue('id')
    await body('[data-testid=send-dialog] form').trigger('submit')
    await flushPromises()
    expect(POST).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/banquet/events/{id}/emails/contract', expect.objectContaining({ body: { to: 'klien@contoh.test', language: 'id' } }))
  })

  it('offers no sending without a mail server, and says so; the contract waits for a definite event', async () => {
    GET = vi.fn(async (path: string) => ({ data: path.endsWith('/emails') ? { enabled: false, data: [] } : { data: [] } }))
    const w = await mountIt(BanquetDocsTab, { propertyId: 7, event: event({ status: 'TENTATIVE' }), canManage: true, canConfirm: true })
    expect(w.find('[data-testid=proposal-send]').exists()).toBe(false)
    expect(w.find('[data-testid=docs-no-mail]').exists()).toBe(true)
    expect(w.find('[data-testid=contract-issue]').exists()).toBe(false)
    expect(w.get('[data-testid=doc-contract]').text()).toContain('once the event is definite')
  })
})

describe('the bell of the banquet', () => {
  const notice = (id: number, kind: string, over: Record<string, unknown> = {}) => ({ id, kind, revision_no: null, event_id: 20, event_number: 'BQ-000020', event_name: 'Pernikahan', session_id: null, notice_date: '2026-10-25', created_at: '2026-10-25T10:00:00Z', seen_at: null, ...over })

  async function mountBell(list: unknown[]) {
    const { pinia, router } = prepare()
    useAuthStore().me = { user: { is_tenant_admin: true }, properties: [] } as never
    useSubscriptionStore().modules = ['BANQUET']
    GET = vi.fn(async () => ({ data: { data: list } }))
    POST = vi.fn(async () => ({ data: { marked: 1 } }))
    await router.push('/')
    const w = mount(BanquetBell, { attachTo: document.body, global: { plugins: [pinia, router] } })
    mounted.push(w)
    await flushPromises()
    return w
  }

  it('counts the notices that are not seen', async () => {
    const w = await mountBell([notice(1, 'BEO_REVISED', { revision_no: 4 }), notice(2, 'OPTION_LOST'), notice(3, 'DEPOSIT_DUE', { seen_at: '2026-10-25T11:00:00Z' })])
    expect(w.get('[data-testid=banquet-bell-count]').text()).toBe('2')
  })

  it('lists the revision of the BEO with its number, and marks the notice seen when it is opened', async () => {
    const w = await mountBell([notice(1, 'BEO_REVISED', { revision_no: 4 })])
    await w.get('[data-testid=banquet-bell]').trigger('click')
    await flushPromises()
    expect(body('[data-testid=banquet-notice-1]').text()).toContain('BQ-000020: the BEO was revised (revision 4)')
    await body('[data-testid=banquet-notice-1]').trigger('click')
    await flushPromises()
    expect(POST).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/banquet/notices/seen', expect.objectContaining({ body: { ids: [1] } }))
  })

  it('is not shown where the module is not in the plan', async () => {
    const { pinia, router } = prepare()
    useAuthStore().me = { user: { is_tenant_admin: true }, properties: [] } as never
    useSubscriptionStore().modules = ['CORE']
    GET = vi.fn(async () => ({ data: { data: [] } }))
    await router.push('/')
    const w = mount(BanquetBell, { attachTo: document.body, global: { plugins: [pinia, router] } })
    mounted.push(w)
    await flushPromises()
    expect(w.find('[data-testid=banquet-bell]').exists()).toBe(false)
  })
})

describe('the transfer dialog when nothing is posted yet', () => {
  it('says "Belum ada tagihan terposting", turns the choice by items off and starts on the amount', async () => {
    setLocale('id')
    GET = vi.fn(async () => ({ data: { items: [] } }))
    const folio = { id: 5, number: 'FOL000005', status: 'OPEN', debit: '0', credit: '0', balance: '500000' }
    const group = { group_id: 3, code: 'GRP1', name: 'Rombongan', folio_id: 9, folio_number: 'FOL000009' }
    const { pinia, router } = prepare()
    await router.push('/')
    const w = mount(BanquetTransferDialog, { attachTo: document.body, props: { open: false, propertyId: 7, event: event(), folio, groups: [group] } as never, global: { plugins: [pinia, router] } })
    mounted.push(w)
    await w.setProps({ open: true })
    await flushPromises()
    expect(body('[data-testid=transfer-items-hint]').text()).toBe('Belum ada tagihan terposting.')
    expect(body('[data-testid=transfer-items-hint]').text()).not.toContain('0 tagihan')
    expect((document.body.querySelector('[data-testid=transfer-mode-items]') as HTMLInputElement).disabled).toBe(true)
    expect((document.body.querySelector('[data-testid=transfer-mode-balance]') as HTMLInputElement).checked).toBe(true)
  })
})

describe('the recipients of the BEO', () => {
  it('marks a department that has neither a head nor a deputy', async () => {
    const recipient = (sec: string, over: Record<string, unknown>) => ({ section: sec, department_id: 1, department_name: 'Dapur', head: 'Budi', deputy: null, ...over })
    GET = vi.fn(async (path: string) => ({
      data: path.endsWith('/beo-recipients')
        ? { data: [recipient('KITCHEN', {}), recipient('HOUSEKEEPING', { department_id: 2, department_name: 'Kamar', head: null, deputy: null }), recipient('SECURITY', { department_id: null, department_name: null, head: null, deputy: null })] }
        : { data: [{ id: 1, code: 'FB', name: 'F&B', level: 1, is_active: true }, { id: 2, code: 'ROOMS', name: 'Rooms', level: 1, is_active: true }] },
    }))
    setLocale('id')
    const w = await mountIt(BanquetRecipientsCard, { propertyId: 7 })
    expect(w.get('[data-testid=recipient-people-HOUSEKEEPING]').text()).toContain('belum punya kepala maupun wakil')
    expect(w.get('[data-testid=recipient-people-HOUSEKEEPING]').classes()).toContain('text-destructive')
    expect(w.get('[data-testid=recipient-people-KITCHEN]').text()).toContain('Kepala: Budi')
    expect(w.get('[data-testid=recipient-people-SECURITY]').text()).toBe('Tidak ada yang diberi tahu.')
  })
})
