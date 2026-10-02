import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import GroupsView from './GroupsView.vue'

let GET = vi.fn()
let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a) } }))

const conf = {
  id: 4, code: 'CONF', name: 'Annual conference', company_id: 1, company_name: 'Acme Corp', arrival_date: '2026-10-10', departure_date: '2026-10-14',
  is_active: true, reservation_count: 3, room_count: 8,
}

function mountView(permissions = ['group.manage', 'reservation.read']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.clock = { business_date: '2026-10-01' } as never
  GET = vi.fn(async (path: string) => ({ data: { data: path.endsWith('/companies') ? [{ id: 1, code: 'ACME', name: 'Acme Corp' }] : [conf] } }))
  POST = vi.fn().mockResolvedValue({ data: {} })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  return mount(GroupsView, { global: { plugins: [pinia, router] } })
}

describe('GroupsView', () => {
  beforeEach(() => {
    GET = vi.fn()
    POST = vi.fn()
  })

  it('lists active groups with their counts', async () => {
    const w = mountView()
    await flushPromises()
    const row = w.get('[data-testid=group-CONF]')
    expect(row.text()).toContain('Acme Corp')
    expect(row.text()).toContain('10 Oct 2026 to 14 Oct 2026')
    expect(row.text()).toContain('8')
    expect(GET.mock.calls[0]?.[1].params.query.active).toBe(true)
    await w.get('input[name=active_only]').setValue(false)
    await flushPromises()
    expect(GET.mock.calls.at(-1)?.[1].params.query.active).toBeUndefined()
  })

  it('creates a group of a company and shows a refusal', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=new-group]').trigger('click')
    await flushPromises()
    expect((w.get('input[name=arrival_date]').element as HTMLInputElement).value).toBe('2026-10-01')
    await w.get('input[name=code]').setValue('WED')
    await w.get('input[name=name]').setValue('Wedding')
    await w.get('input[name=departure_date]').setValue('2026-10-03')
    await w.get('select[name=company_id]').setValue(1)
    await w.get('[data-testid=group-form]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls[0]?.[1].body).toMatchObject({ code: 'WED', name: 'Wedding', company_id: 1, arrival_date: '2026-10-01', departure_date: '2026-10-03', is_active: true })
    await w.get('[data-testid=new-group]').trigger('click')
    await w.get('[data-testid=group-form]').trigger('submit')
    expect(w.find('[data-testid=dates-required]').exists()).toBe(true) // an empty date is never sent
    expect(POST).toHaveBeenCalledTimes(1)
    await w.get('input[name=departure_date]').setValue('2026-10-01')
    POST.mockRejectedValue(new ApiError({ type: 't', title: 'Invalid', status: 422, code: 'VALIDATION_FAILED', detail: 'the group is invalid', errors: [{ field: 'departure_date', code: 'INVALID_RANGE', message: 'after the arrival date' }] } as never))
    await w.get('[data-testid=group-form]').trigger('submit')
    await flushPromises()
    expect(w.get('[data-testid=group-form] [role=alert]').text()).toContain('after the arrival date')
  })

  it('needs group.manage to create and reservation.read to see', async () => {
    const reader = mountView(['reservation.read'])
    await flushPromises()
    expect(reader.find('[data-testid=new-group]').exists()).toBe(false)
    const none = mountView([])
    await flushPromises()
    expect(none.find('[data-testid=no-access]').exists()).toBe(true)
  })
})
