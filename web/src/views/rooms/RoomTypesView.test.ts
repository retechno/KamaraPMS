import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import RoomTypesView from './RoomTypesView.vue'

let GET = vi.fn()
let POST = vi.fn()
let PATCH = vi.fn()
vi.mock('@/api/client', () => ({
  api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a), PATCH: (...a: unknown[]) => PATCH(...a) },
}))

const types = [
  { id: 2, code: 'STD', name: 'Standard', max_adult: 2, max_child: 1, max_occupancy: 3, base_occupancy: 2, sort_order: 2, is_active: true },
  { id: 1, code: 'DLX', name: 'Deluxe', max_adult: 2, max_child: 2, max_occupancy: 4, base_occupancy: 2, sort_order: 1, is_active: false },
]

function mountTypes(permissions: string[]) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn(async () => ({ data: { data: types } }))
  POST = vi.fn().mockResolvedValue({ data: {} })
  PATCH = vi.fn().mockResolvedValue({ data: {} })
  return mount(RoomTypesView, { global: { plugins: [pinia] } })
}

describe('RoomTypesView', () => {
  beforeEach(() => {
    GET = vi.fn()
    POST = vi.fn()
    PATCH = vi.fn()
  })

  it('lists the types in their sort order with their capacity', async () => {
    const w = mountTypes(['room.manage'])
    await flushPromises()
    expect(w.findAll('tbody tr').map((r) => r.attributes('data-testid'))).toEqual(['type-DLX', 'type-STD'])
    expect(w.get('[data-testid=type-STD]').text()).toContain('2 / 1')
  })

  it('creates a type', async () => {
    const w = mountTypes(['room.manage'])
    await flushPromises()
    await w.get('[data-testid=new-type]').trigger('click')
    await w.get('input[name=code]').setValue('SUP')
    await w.get('input[name=name]').setValue('Superior')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(POST).toHaveBeenCalledWith(
      '/api/v1/properties/{propertyId}/room-types',
      expect.objectContaining({ body: expect.objectContaining({ code: 'SUP', name: 'Superior', max_adult: 2, is_active: true }) }),
    )
  })

  it('hides editing without room.manage', async () => {
    const w = mountTypes([])
    await flushPromises()
    expect(w.find('[data-testid=new-type]').exists()).toBe(false)
    expect(w.find('[data-testid=type-STD] button').exists()).toBe(false)
  })
})
