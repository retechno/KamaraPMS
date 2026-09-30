import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import ReservationsView from './ReservationsView.vue'

let GET = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a) } }))

const row = { id: 1, confirmation_number: 'RES000001', guest_id: 3, guest_name: 'Siti Nurhaliza', source: 'PHONE', status: 'CONFIRMED', arrival_date: '2026-10-02', departure_date: '2026-10-04', room_count: 1, version: 2 }

function mountView(permissions: string[], page: object = { data: [row] }) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn().mockResolvedValue({ data: page })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  return mount(ReservationsView, { global: { plugins: [pinia, router] } })
}

describe('ReservationsView', () => {
  beforeEach(() => {
    GET = vi.fn()
  })

  it('lists reservations on open, with a link to each', async () => {
    const w = mountView(['reservation.read', 'reservation.create'])
    await flushPromises()
    expect(GET.mock.calls[0]?.[1]).toMatchObject({ params: { path: { propertyId: 7 }, query: { limit: 50 } } })
    const r = w.get('[data-testid=res-RES000001]')
    expect(r.text()).toContain('Siti Nurhaliza')
    expect(r.text()).toContain('Confirmed')
    expect(r.get('a').attributes('href')).toBe('/reservations/1')
    expect(w.find('[data-testid=new]').exists()).toBe(true)
  })

  it('filters by text, status and arrival dates', async () => {
    const w = mountView(['reservation.read'])
    await flushPromises()
    expect(w.find('[data-testid=new]').exists()).toBe(false) // no create permission
    await w.get('input[name=q]').setValue(' RES0 ')
    await w.get('select[name=status]').setValue('CANCELLED')
    await w.get('input[name=arrival_from]').setValue('2026-10-01')
    await w.get('form[role=search]').trigger('submit')
    await flushPromises()
    expect(GET.mock.calls.at(-1)?.[1]).toMatchObject({ params: { query: { q: 'RES0', status: 'CANCELLED', arrival_from: '2026-10-01', arrival_to: undefined } } })
  })

  it('loads more with the cursor and says when nothing matches', async () => {
    const w = mountView(['reservation.read'], { data: [row], next_cursor: 'abc' })
    await flushPromises()
    GET.mockResolvedValue({ data: { data: [{ ...row, id: 2, confirmation_number: 'RES000002' }] } })
    await w.get('[data-testid=more]').trigger('click')
    await flushPromises()
    expect(GET.mock.calls.at(-1)?.[1]).toMatchObject({ params: { query: { cursor: 'abc' } } })
    expect(w.findAll('tbody tr')).toHaveLength(2)
    GET.mockResolvedValue({ data: { data: [] } })
    await w.get('form[role=search]').trigger('submit')
    await flushPromises()
    expect(w.get('[data-testid=empty]').text()).toContain('No reservations')
  })

  it('needs read permission', async () => {
    const w = mountView(['guest.read'])
    await flushPromises()
    expect(w.find('[data-testid=no-access]').exists()).toBe(true)
    expect(GET).not.toHaveBeenCalled()
  })
})
