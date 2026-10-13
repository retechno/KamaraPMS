import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { usePropertyStore } from '@/stores/property'
import CheckOutWizard from './CheckOutWizard.vue'

let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { POST: (...a: unknown[]) => POST(...a) } }))

const detail = (departure: string, balance = '0') => ({
  stay: { id: 5, stay_number: 'STY000001', arrival_date: '2026-09-30', departure_date: departure, status: 'OPEN', version: 3 },
  folios: [{ id: 8, folio_number: 'FOL000001', status: 'OPEN', balance }],
}) as never

function mountWizard(d: never) {
  const pinia = createPinia()
  setActivePinia(pinia)
  const property = usePropertyStore()
  property.currentId = 7
  property.clock = { business_date: '2026-09-30' } as never
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  return mount(CheckOutWizard, { props: { detail: d }, global: { plugins: [pinia, router] } })
}

const result = { stay: { id: 5, status: 'CHECKED_OUT' }, posted_room_charges: [{ service_date: '2026-09-30', total: '1100000' }], folios: [{ id: 8, folio_number: 'FOL000001', status: 'CLOSED' }], housekeeping: 'DIRTY' }

describe('CheckOutWizard', () => {
  beforeEach(() => {
    POST = vi.fn().mockResolvedValue({ data: result })
  })

  it('checks out on the departure day with the stay version and an idempotency key', async () => {
    const w = mountWizard(detail('2026-09-30'))
    await w.get('[data-testid=checkout-submit]').trigger('click')
    await flushPromises()
    const call = POST.mock.calls[0]
    expect(call?.[0]).toBe('/api/v1/properties/{propertyId}/stays/{id}/check-out')
    expect(call?.[1]).toMatchObject({ params: { path: { propertyId: 7, id: 5 } }, body: { version: 3, confirm_early_departure: false } })
    expect(typeof call?.[1].params.header['Idempotency-Key']).toBe('string')
    expect(w.get('[data-testid=checkout-done]').text()).toContain('DIRTY')
    expect(w.emitted('done')).toHaveLength(1)
  })

  it('says that the group still has an open bill when this was its last room, and only then', async () => {
    const w = mountWizard(detail('2026-09-30'))
    POST.mockResolvedValue({ data: { ...result, master_folio: { folio_id: 30, folio_number: 'FOL000030', balance: '2000000', group_code: 'GRP1', group_name: 'Conference' } } })
    await w.get('[data-testid=checkout-submit]').trigger('click')
    await flushPromises()
    const note = w.get('[data-testid=checkout-master]')
    expect(note.text()).toContain('This was the last room of group GRP1: its master folio FOL000030 is still open (balance 2,000,000).')
    expect(note.get('a').attributes('href')).toBe('/folios/30')
    const other = mountWizard(detail('2026-09-30'))
    POST.mockResolvedValue({ data: { ...result, master_folio: null } })
    await other.get('[data-testid=checkout-submit]').trigger('click')
    await flushPromises()
    expect(other.find('[data-testid=checkout-master]').exists()).toBe(false)
  })

  it('needs an explicit confirmation for an early departure', async () => {
    const w = mountWizard(detail('2026-10-03'))
    expect(w.get('[data-testid=checkout-submit]').attributes('disabled')).toBeDefined()
    await w.get('input[name=confirm_early]').setValue(true)
    await w.get('[data-testid=checkout-submit]').trigger('click')
    await flushPromises()
    expect(POST.mock.calls[0]?.[1]).toMatchObject({ body: { version: 3, confirm_early_departure: true } })
  })

  it('points to the folios that still have a balance and keeps the dialog open on a refusal', async () => {
    const w = mountWizard(detail('2026-09-30', '-100000'))
    expect(w.get('[data-testid=open-folio-8]').attributes('href')).toBe('/folios/8')
    POST.mockRejectedValue(new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'FOLIO_NOT_BALANCED', detail: 'not balanced' }))
    await w.get('[data-testid=checkout-submit]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid=checkout-error]').text()).toContain('FOLIO_NOT_BALANCED')
    expect(w.find('[data-testid=checkout-done]').exists()).toBe(false)
    expect(w.emitted('done')).toBeUndefined()
  })
})
