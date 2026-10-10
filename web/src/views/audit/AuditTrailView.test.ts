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

const USERS = [{ id: 9, full_name: 'Wayan' }, { id: 3, full_name: 'Siti' }]
const auditCalls = () => GET.mock.calls.filter((c) => c[0] === '/api/v1/properties/{propertyId}/audit-logs')

function mountView(permissions = ['audit.read'], page: object = { data: [entry(2), entry(1, { user: null, action: 'business_day.closed' })], next_cursor: 'c1' }, usersFail = false) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn(async (path: string) => {
    if (path === '/api/v1/users') {
      if (usersFail) throw new ApiError({ type: 't', title: 'Forbidden', status: 403, code: 'PERMISSION_DENIED', detail: 'x' })
      return { data: { data: USERS } }
    }
    if (path === '/api/v1/properties/{propertyId}/reservations') return { data: { data: [{ id: 41, confirmation_number: 'RES000041' }] } }
    return { data: page }
  })
  return mount(AuditTrailView, { global: { plugins: [pinia] } })
}

/** Runs `fn` with the window of a phone (390 px), then puts the width back. */
async function onAPhone<T>(fn: () => Promise<T>): Promise<T> {
  const wide = window.innerWidth
  Object.defineProperty(window, 'innerWidth', { configurable: true, value: 390 })
  try {
    return await fn()
  } finally {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: wide })
  }
}

describe('AuditTrailView', () => {
  beforeEach(() => {
    GET = vi.fn()
    setDisplayTimeZone('Asia/Jakarta')
  })

  it('lists the newest entries, names the user (system when none) and shows before and after on demand', async () => {
    const w = mountView()
    await flushPromises()
    expect(auditCalls()[0]).toEqual(['/api/v1/properties/{propertyId}/audit-logs', { params: { path: { propertyId: 7 }, query: { limit: 50, cursor: undefined } } }])
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
    await w.get('select[name=entity_type]').setValue('stay')
    await w.get('select[name=action]').setValue('stay.checked_in')
    await w.get('select[name=user_id]').setValue('3')
    await w.get('input[name=from]').setValue('2026-09-30')
    await w.get('[data-testid=filters]').trigger('submit')
    await flushPromises()
    expect(auditCalls().at(-1)?.[1]).toMatchObject({ params: { query: { entity_type: 'stay', action: 'stay.checked_in', user_id: 3, from: '2026-09-30', limit: 50 } } })
    expect(auditCalls().at(-1)?.[1].params.query.to).toBeUndefined()
    GET.mockResolvedValue({ data: { data: [entry(0)] } })
    await w.get('[data-testid=more]').trigger('click')
    await flushPromises()
    expect(auditCalls().at(-1)?.[1]).toMatchObject({ params: { query: { cursor: 'c1', entity_type: 'stay' } } })
    expect(w.findAll('[data-testid^=entry-]').length).toBe(1 + 2) // 2 from the search, 1 more
    await w.get('[data-testid=reset]').trigger('click')
    await flushPromises()
    expect((w.get('select[name=entity_type]').element as HTMLSelectElement).value).toBe('')
    expect(auditCalls().at(-1)?.[1].params.query.entity_type).toBeUndefined()
  })

  it('offers the entities, actions and people in words, never as codes', async () => {
    const w = mountView()
    await flushPromises()
    const texts = (name: string) => w.findAll(`select[name=${name}] option`).map((o) => o.text())
    expect(texts('entity_type')).toContain('Reservation')
    expect(texts('entity_type')).toContain('Cashier shift')
    expect(texts('action')).toContain('Payment posted')
    expect(texts('action')).toContain('Checked in')
    expect(texts('user_id')).toEqual(['All users', 'Siti', 'Wayan'])
    for (const text of [...texts('entity_type'), ...texts('action')]) expect(text).not.toMatch(/[a-z]+[._][a-z]+/)
    expect(w.get('input[name=document]').attributes('placeholder')).toBe('RES000123, STY000035…')
  })

  it('leaves the user filter out when the people cannot be listed', async () => {
    const w = mountView(['audit.read'], { data: [entry(2)] }, true)
    await flushPromises()
    expect(w.find('select[name=user_id]').exists()).toBe(false)
    expect(w.find('select[name=action]').exists()).toBe(true)
  })

  it('looks a reservation number up and asks the server for that reservation', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('input[name=document]').setValue('res000041')
    await w.get('[data-testid=filters]').trigger('submit')
    await flushPromises()
    expect(GET).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/reservations', { params: { path: { propertyId: 7 }, query: { q: 'RES000041', limit: 5 } } })
    expect(auditCalls().at(-1)?.[1]).toMatchObject({ params: { query: { entity_type: 'reservation', entity_id: 41 } } })
  })

  it('says so when no reservation has the number', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('input[name=document]').setValue('RES999999')
    await w.get('[data-testid=filters]').trigger('submit')
    await flushPromises()
    expect(w.get('[data-testid=empty]').text()).toContain('No reservation RES999999.')
  })

  it('finds another document by the number its entries carry, reading page after page', async () => {
    const pages: Record<string, object> = {
      first: { data: [entry(10, { new_data: { payment_number: 'PAY000001' } }), entry(11, { new_data: { payment_number: 'PAY000032' } })], next_cursor: 'p2' },
      p2: { data: [entry(12, { new_data: { folio_id: 3 } }), entry(13, { old_data: { payment_number: 'PAY000032' } })], next_cursor: undefined },
    }
    const w = mountView()
    await flushPromises()
    GET.mockImplementation(async (path: string, opts: { params: { query: { cursor?: string } } }) => (path === '/api/v1/users' ? { data: { data: USERS } } : { data: pages[opts.params.query.cursor ?? 'first'] }))
    await w.get('input[name=document]').setValue('pay000032')
    await w.get('[data-testid=filters]').trigger('submit')
    await flushPromises()
    expect(w.findAll('[data-testid^=entry-]').map((r) => r.attributes('data-testid'))).toEqual(['entry-11', 'entry-13'])
    expect(w.find('[data-testid=scan-capped]').exists()).toBe(false) // it read to the end
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

  it('is a card on a phone: the action as the title, the time under it, and "Details" opens the entry under the card', async () => {
    await onAPhone(async () => {
      const w = mountView()
      await flushPromises()
      expect(w.find('table').exists()).toBe(false)
      const card = w.get('[data-testid=entry-2]')
      expect(card.text()).toContain('Checked in')
      expect(card.text()).toContain('30 Sep 2026 20:00')
      expect(w.find('[data-testid=detail-2]').exists()).toBe(false)
      await card.get('[data-testid=toggle-2]').trigger('click')
      expect(w.get('[data-testid=detail-2]').text()).toContain('"status": "OPEN"')
      expect(card.get('[data-testid=toggle-2]').text()).toBe('Hide')
      w.unmount()
    })
  })

  it('counts what is loaded and loads the next page', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.get('[data-testid=table-count]').text()).toBe('2 loaded')
    expect(w.get('[data-testid=more]').text()).toBe('Load more')
  })
})
