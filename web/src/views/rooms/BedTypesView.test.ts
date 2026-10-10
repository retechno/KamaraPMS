import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import BedTypesView from './BedTypesView.vue'

let GET = vi.fn()
let POST = vi.fn()
let PATCH = vi.fn()
vi.mock('@/api/client', () => ({
  api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a), PATCH: (...a: unknown[]) => PATCH(...a) },
}))

const beds = [
  { id: 5, code: 'KING', name: 'King', sort_order: 10, is_active: true },
  { id: 6, code: 'TWIN', name: 'Twin', sort_order: 40, is_active: true },
  { id: 7, code: 'SOFA', name: 'Sofa bed', sort_order: 60, is_active: false },
]

function mountBeds(permissions: string[]) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn(async () => ({ data: { data: beds } }))
  POST = vi.fn().mockResolvedValue({ data: {} })
  PATCH = vi.fn().mockResolvedValue({ data: {} })
  return mount(BedTypesView, { global: { plugins: [pinia] } })
}

describe('BedTypesView', () => {
  beforeEach(() => {
    GET = vi.fn()
    POST = vi.fn()
    PATCH = vi.fn()
  })

  it('lists the bed types with their state', async () => {
    const w = mountBeds(['room.manage'])
    await flushPromises()
    expect(GET).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/bed-types', { params: { path: { propertyId: 7 } } })
    expect(w.findAll('tbody tr').map((r) => r.attributes('data-testid'))).toEqual(['bed-KING', 'bed-TWIN', 'bed-SOFA'])
    expect(w.get('[data-testid=bed-SOFA]').text()).toContain('Inactive')
  })

  it('adds a bed type after the last one', async () => {
    const w = mountBeds(['room.manage'])
    await flushPromises()
    await w.get('[data-testid=new-bed]').trigger('click')
    expect((w.get('input[name=sort_order]').element as HTMLInputElement).value).toBe('70')
    await w.get('input[name=code]').setValue('QUEEN')
    await w.get('input[name=name]').setValue('Queen')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(POST).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/bed-types', {
      params: { path: { propertyId: 7 } }, body: { code: 'QUEEN', name: 'Queen', sort_order: 70, is_active: true },
    })
  })

  it('edits a bed type without its code and shows a refusal next to the field', async () => {
    const w = mountBeds(['room.manage'])
    await flushPromises()
    await w.get('[data-testid=bed-KING] button').trigger('click')
    expect(w.get('input[name=code]').attributes('disabled')).toBeDefined()
    PATCH.mockRejectedValue(new ApiError({ type: 't', title: 'Unprocessable', status: 422, code: 'VALIDATION_FAILED', detail: 'invalid', errors: [{ field: 'name', code: 'REQUIRED', message: 'a name is needed' }] }))
    await w.get('input[name=name]').setValue('')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(w.get('[data-testid=form-error]').text()).toContain('VALIDATION_FAILED')
    expect(w.get('form').text()).toContain('a name is needed')
    PATCH.mockResolvedValue({ data: {} })
    await w.get('input[name=name]').setValue('King size')
    await w.get('input[name=is_active]').setValue(false)
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(PATCH.mock.calls.at(-1)?.[1]).toEqual({ params: { path: { propertyId: 7, id: 5 } }, body: { name: 'King size', sort_order: 10, is_active: false } })
  })

  it('hides editing without room.manage', async () => {
    const w = mountBeds([])
    await flushPromises()
    expect(w.find('[data-testid=new-bed]').exists()).toBe(false)
    expect(w.find('[data-testid=bed-KING] button').exists()).toBe(false)
  })

  it('has an empty state that says what bed types are and offers to add the first one, to a person who may manage rooms', async () => {
    const saved = beds.splice(0)
    try {
      const w = mountBeds(['room.manage'])
      await flushPromises()
      const empty = w.get('[data-testid=empty]')
      expect(empty.text()).toContain('Bed types say which beds a room has.')
      const action = empty.get('[data-testid=empty-action]')
      expect(action.text()).toBe('New bed type')
      await action.trigger('click')
      expect(w.find('input[name=code]').exists()).toBe(true) // the form of the new bed type
      w.unmount()
      const reader = mountBeds([])
      await flushPromises()
      expect(reader.get('[data-testid=empty]').text()).toContain('Bed types say which beds a room has.')
      expect(reader.find('[data-testid=empty-action]').exists()).toBe(false)
    } finally {
      beds.push(...saved)
    }
  })
})
