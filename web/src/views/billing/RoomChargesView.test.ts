import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import RoomChargesView from './RoomChargesView.vue'

let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { POST: (...a: unknown[]) => POST(...a) } }))

const item = (over: object = {}) => ({
  stay_id: 5, stay_number: 'STY000001', guest: 'Siti Nurhaliza', room_number: '101', folio_id: 8, service_date: '2026-10-01', charge_code: 'ROOM', room_rate: '1000000',
  price_mode: 'EXCLUSIVE', service_charge: '100000', tax: '121000', rounding_adjustment: '0', total: '1221000', status: 'READY', folio_item_id: null, ...over,
})
const preview = (items: object[]) => ({
  business_date: '2026-10-01', items, totals: { ready_count: items.filter((i) => (i as { status: string }).status === 'READY').length, ready_total: '2442000' },
})

function mountView(permissions = ['folio.post_charge'], pv: object = preview([item({ service_date: '2026-09-30' }), item()])) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.clock = { business_date: '2026-10-01' } as never
  POST = vi.fn(async (path: string) => ({ data: path.endsWith('/preview') ? pv : { results: [{ stay_id: 5, service_date: '2026-10-01', status: 'POSTED', total: '1221000' }], revalidation: { ready: 0, errors: [], invalid: [] } } }))
  return mount(RoomChargesView)
}

describe('RoomChargesView', () => {
  beforeEach(() => {
    POST = vi.fn()
  })

  it('previews the nights that are due and marks the missing ones', async () => {
    const w = mountView()
    await flushPromises()
    expect(POST.mock.calls[0]).toEqual(['/api/v1/properties/{propertyId}/night-audit/room-charges/preview', { params: { path: { propertyId: 7 } }, body: { business_date: '2026-10-01' } }])
    expect(w.get('[data-testid=ready-count]').text()).toBe('2 night(s) ready')
    expect(w.get('[data-testid=ready-total]').text()).toBe('2,442,000')
    expect(w.get('[data-testid=item-5-2026-09-30]').text()).toContain('missing')
    expect(w.get('[data-testid=item-5-2026-10-01]').text()).toContain('1,221,000')
  })

  it('posts with an Idempotency-Key and shows what happened', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=post]').trigger('click')
    await flushPromises()
    const call = POST.mock.calls.find(([p]) => !(p as string).endsWith('/preview')) as [string, { params: { header: Record<string, string> }; body: object }]
    expect(call[0]).toBe('/api/v1/properties/{propertyId}/night-audit/room-charges')
    expect(call[1].params.header['Idempotency-Key']).toBeTruthy()
    expect(call[1].body).toEqual({ business_date: '2026-10-01' })
    expect(w.get('[data-testid=outcome]').text()).toContain('1 night(s) posted')
    expect(POST.mock.calls.at(-1)?.[0]).toContain('/preview') // the preview is refreshed
  })

  it('shows problems and offers nothing to post when nothing is ready', async () => {
    const w = mountView(['nightaudit.run'], preview([item({ status: 'ERROR', reason: 'NO_OPEN_FOLIO' }), item({ service_date: '2026-09-30', status: 'ALREADY_POSTED' })]))
    await flushPromises()
    expect(w.find('[data-testid=post]').exists()).toBe(false)
    expect(w.get('[data-testid=problems]').text()).toContain('1 night(s) cannot be posted')
    expect(w.get('[data-testid=item-5-2026-10-01]').text()).toContain('NO_OPEN_FOLIO')
  })

  it('says when nobody is in house, shows server errors, and needs a permission', async () => {
    const empty = mountView(['folio.post_charge'], preview([]))
    await flushPromises()
    expect(empty.get('[data-testid=empty]').text()).toContain('Nobody')
    const denied = mountView(['guest.read'])
    await flushPromises()
    expect(denied.find('[data-testid=no-access]').exists()).toBe(true)
    expect(POST).not.toHaveBeenCalled()

    const w = mountView()
    await flushPromises()
    POST.mockRejectedValue(new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'BUSINESS_DATE_MISMATCH', detail: 'the business date has changed' }))
    await w.get('[data-testid=post]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid=form-error]').text()).toContain('BUSINESS_DATE_MISMATCH')
  })
})
