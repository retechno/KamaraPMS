import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { resetDepartments } from '@/composables/useDepartments'
import { usePropertyStore } from '@/stores/property'
import DepartmentSelect from './DepartmentSelect.vue'

let GET = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a) } }))

const dept = (over: Record<string, unknown>) => ({ id: 1, parent_id: null, code: 'ROOMS', name: 'Rooms', sort_order: 10, is_active: true, level: 1, child_count: 0, in_use: true, ...over })
const departments = [
  dept({}),
  dept({ id: 2, code: 'FB', name: 'Food and beverage', child_count: 1 }),
  dept({ id: 9, parent_id: 2, code: 'REST', name: 'Restaurant', level: 2 }),
  dept({ id: 3, code: 'SPA', name: 'Spa', is_active: false }),
]

function mountSelect(modelValue: number | null = null) {
  const pinia = createPinia()
  setActivePinia(pinia)
  usePropertyStore().currentId = 7
  return mount(DepartmentSelect, { props: { modelValue, 'onUpdate:modelValue': () => {} }, attrs: { name: 'department_id' }, global: { plugins: [pinia] } })
}

describe('DepartmentSelect', () => {
  beforeEach(() => {
    resetDepartments()
    GET = vi.fn().mockResolvedValue({ data: { data: departments } })
  })

  it('offers none, the departments and their sub-departments in use', async () => {
    const w = mountSelect()
    await flushPromises()
    expect(GET.mock.calls[0]?.[1].params.path).toEqual({ propertyId: 7 })
    expect(w.findAll('option').map((o) => o.text())).toEqual(['No department', 'ROOMS · Rooms', 'FB · Food and beverage', '– REST · Restaurant'])
    expect(w.get('select').attributes('name')).toBe('department_id')
  })

  it('chooses a department by its id, and none again', async () => {
    const w = mountSelect()
    await flushPromises()
    await w.get('select').setValue('9')
    expect(w.emitted('update:modelValue')?.at(-1)).toEqual([9])
    await w.get('select').setValue('')
    expect(w.emitted('update:modelValue')?.at(-1)).toEqual([null])
  })

  it('shows a department that is switched off only while it is the one chosen', async () => {
    const w = mountSelect(3)
    await flushPromises()
    expect(w.findAll('option').map((o) => o.text())).toContain('SPA · Spa (Switched off)')
    expect((w.get('select').element as HTMLSelectElement).value).toBe('3')
  })

  it('is not shown to a role that cannot read the departments', async () => {
    GET = vi.fn().mockRejectedValue(new Error('403'))
    const w = mountSelect()
    await flushPromises()
    expect(w.find('select').exists()).toBe(false)
  })

  it('reads the departments once for all the lines of a form', async () => {
    mountSelect()
    mountSelect()
    mountSelect()
    await flushPromises()
    expect(GET).toHaveBeenCalledTimes(1)
  })
})
