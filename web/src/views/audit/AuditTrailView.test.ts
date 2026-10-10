import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { setDisplayTimeZone } from '@/utils/format'
import AuditTrailView from './AuditTrailView.vue'

let GET = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a) } }))

const entry = (id: number, over: object = {}) => ({
  id, created_at: '2026-09-30T13:00:00.123Z', business_date: '2026-09-30', user: { id: 3, name: 'Siti' }, action: 'stay.checked_in', entity_type: 'stay', entity_id: 5,
  old_data: null, new_data: { status: 'OPEN', password_hash: '[redacted]' }, request_id: 'req1', ...over,
})

function mountView(permissions = ['audit.read'], page: object = { data: [entry(2), entry(1, { user: null, action: 'business_day.closed' })], next_cursor: 'c1' }) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn().mockResolvedValue({ data: page })
  return mount(AuditTrailView, { global: { plugins: [pinia] } })
}

describe('AuditTrailView', () => {
  beforeEach(() => {
    GET = vi.fn()
    setDisplayTimeZone('Asia/Jakarta')
  })

  it('lists the newest entries, names the user (system when none) and shows before and after on demand', async () => {
    const w = mountView()
    await flushPromises()
    expect(GET.mock.calls[0]).toEqual(['/api/v1/properties/{propertyId}/audit-logs', { params: { path: { propertyId: 7 }, query: { limit: 50, cursor: undefined } } }])
    expect(w.get('[data-testid=entry-2]').text()).toContain('Siti')
    expect(w.get('[data-testid=entry-2]').text()).toContain('30 Sep 2026 20:00') // 13:00 UTC on the clock of the property (Jakarta), without seconds
    expect(w.get('[data-testid=entry-2]').text()).toContain('Checked in') // the action in words; the code stays in the tooltip
    expect(w.get('[data-testid=entry-2]').text()).not.toContain('stay.checked_in')
    expect(w.get('[data-testid=entry-2]').text()).toContain('Stay #5') // no number in the entry: the id
    expect(w.get('[data-testid=entry-1]').text()).toContain('system')
    expect(w.find('[data-testid=detail-2]').exists()).toBe(false)
    await w.get('[data-testid=toggle-2]').trigger('click')
    expect(w.get('[data-testid=detail-2]').text()).toContain('"status": "OPEN"')
    await w.get('[data-testid=toggle-2]').trigger('click')
    expect(w.find('[data-testid=detail-2]').exists()).toBe(false)
  })

  it('searches with the filled filters only, clears them, and loads more with the cursor', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('input[name=entity_type]').setValue('stay')
    await w.get('input[name=entity_id]').setValue('5')
    await w.get('input[name=from]').setValue('2026-09-30')
    await w.get('[data-testid=filters]').trigger('submit')
    await flushPromises()
    expect(GET.mock.calls.at(-1)?.[1]).toMatchObject({ params: { query: { entity_type: 'stay', entity_id: 5, from: '2026-09-30', limit: 50 } } })
    expect(GET.mock.calls.at(-1)?.[1].params.query.action).toBeUndefined()
    GET.mockResolvedValue({ data: { data: [entry(0)] } })
    await w.get('[data-testid=more]').trigger('click')
    await flushPromises()
    expect(GET.mock.calls.at(-1)?.[1]).toMatchObject({ params: { query: { cursor: 'c1', entity_id: 5 } } })
    expect(w.findAll('[data-testid^=entry-]').length).toBe(1 + 2) // 2 from the search, 1 more
    await w.get('[data-testid=reset]').trigger('click')
    await flushPromises()
    expect((w.get('input[name=entity_type]').element as HTMLInputElement).value).toBe('')
    expect(GET.mock.calls.at(-1)?.[1].params.query.entity_type).toBeUndefined()
  })

  it('says when nothing matches, shows the server refusal and needs audit.read', async () => {
    const empty = mountView(['audit.read'], { data: [] })
    await flushPromises()
    expect(empty.find('[data-testid=empty]').exists()).toBe(true)
    GET.mockRejectedValue(new ApiError({ type: 't', title: 'Invalid', status: 422, code: 'VALIDATION_FAILED', detail: 'x' }))
    await empty.get('[data-testid=filters]').trigger('submit')
    await flushPromises()
    expect(empty.get('[data-testid=form-error]').text()).toContain('VALIDATION_FAILED')
    const denied = mountView(['guest.read'])
    await flushPromises()
    expect(denied.find('[data-testid=no-access]').exists()).toBe(true)
    expect(GET).not.toHaveBeenCalled()
  })

  it('names what an entry is about by its own number when it carries one, and spells a code it has no words for', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const w = mountView(['audit.read'], { data: [entry(3, { new_data: { stay_number: 'STY000035' }, action: 'payment.posted', entity_type: 'stay' }), entry(4, { action: 'thing.did_something_new' })], next_cursor: null })
    await flushPromises()
    expect(w.get('[data-testid=entry-3]').text()).toContain('Stay STY000035') // its own number, not the id
    expect(w.get('[data-testid=entry-3]').text()).toContain('Payment posted')
    expect(w.get('[data-testid=entry-4]').text()).toContain('Thing did something new')
    expect(warn).toHaveBeenCalledWith(expect.stringContaining('auditAction.thing_did_something_new'))
    warn.mockRestore()
  })
})
