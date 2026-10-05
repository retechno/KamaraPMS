import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import DepartmentsView from './DepartmentsView.vue'

let GET = vi.fn()
let POST = vi.fn()
let PATCH = vi.fn()
let DELETE = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a), PATCH: (...a: unknown[]) => PATCH(...a), DELETE: (...a: unknown[]) => DELETE(...a) } }))

const dept = (over: Record<string, unknown>) => ({ id: 1, parent_id: null, code: 'ROOMS', name: 'Rooms', sort_order: 10, is_active: true, level: 1, child_count: 0, in_use: true, ...over })
const departments = [
  dept({}),
  dept({ id: 2, code: 'FB', name: 'Food and beverage', sort_order: 20, child_count: 1, in_use: false }),
  dept({ id: 9, parent_id: 2, parent_code: 'FB', code: 'REST', name: 'Restaurant', level: 2, in_use: false }),
  dept({ id: 3, code: 'SPA', name: 'Spa', sort_order: 30, is_active: false, in_use: false }),
]

function mountView(permissions = ['accounting.view', 'accounting.manage']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'a@b.c', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn().mockResolvedValue({ data: { data: departments } })
  POST = vi.fn().mockResolvedValue({ data: dept({ id: 10 }) })
  PATCH = vi.fn().mockResolvedValue({ data: dept({}) })
  DELETE = vi.fn().mockResolvedValue({})
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  return mount(DepartmentsView, { global: { plugins: [pinia, router] } })
}

describe('DepartmentsView', () => {
  beforeEach(() => vi.restoreAllMocks())

  it('shows the departments with their sub-departments and state', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.get('[data-testid=department-ROOMS]').attributes('data-level')).toBe('1')
    expect(w.get('[data-testid=department-REST]').attributes('data-level')).toBe('2')
    expect(w.get('[data-testid=department-REST]').text()).toContain('Restaurant')
    expect(w.get('[data-testid=department-SPA]').text()).toContain('Switched off')
    expect(w.get('[data-testid=department-ROOMS]').text()).toContain('In use')
    expect(w.findAll('tbody tr').map((r) => r.attributes('data-testid'))).toEqual(['department-ROOMS', 'department-FB', 'department-REST', 'department-SPA'])
  })

  it('offers what may be done to each one', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.find('[data-testid=sub-FB]').exists()).toBe(true)
    expect(w.find('[data-testid=sub-REST]').exists()).toBe(false) // two levels at most
    expect(w.find('[data-testid=sub-SPA]').exists()).toBe(false) // not under one that is off
    expect(w.find('[data-testid=delete-ROOMS]').exists()).toBe(false) // in use
    expect(w.find('[data-testid=delete-FB]').exists()).toBe(false) // has a sub-department
    expect(w.find('[data-testid=delete-REST]').exists()).toBe(true)
    expect(w.get('[data-testid=toggle-SPA]').text()).toBe('Switch on')
  })

  it('adds a department, and a sub-department of one', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=add]').trigger('click')
    expect(w.findAll('select[name=parent_id] option').map((o) => o.text())).toEqual(['None: a department', 'ROOMS · Rooms', 'FB · Food and beverage']) // only departments in use
    await w.get('input[name=code]').setValue('ag2')
    await w.get('input[name=name]').setValue(' Admin ')
    await w.get('[data-testid=add-form]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls[0]?.[0]).toBe('/api/v1/properties/{propertyId}/departments')
    expect(POST.mock.calls[0]?.[1].body).toEqual({ code: 'ag2', name: 'Admin', parent_id: null })
    expect(w.get('[data-testid=notice]').text()).toContain('AG2 was added')
    await w.get('[data-testid=sub-FB]').trigger('click')
    expect((w.get('select[name=parent_id]').element as HTMLSelectElement).value).toBe('2')
    await w.get('input[name=code]').setValue('BAR')
    await w.get('input[name=name]').setValue('Bar')
    await w.get('input[name=sort_order]').setValue('2')
    await w.get('[data-testid=add-form]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls[1]?.[1].body).toEqual({ code: 'BAR', name: 'Bar', parent_id: 2, sort_order: 2 })
  })

  it('shows why a department was refused', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=add]').trigger('click')
    await w.get('input[name=code]').setValue('ROOMS')
    await w.get('input[name=name]').setValue('Again')
    POST.mockRejectedValueOnce(new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'CODE_TAKEN', detail: 'the department code is already in use' }))
    await w.get('[data-testid=add-form]').trigger('submit')
    await flushPromises()
    expect(w.get('[data-testid=department-error]').text()).toContain('CODE_TAKEN')
    expect(w.find('[data-testid=add-form]').exists()).toBe(true)
  })

  it('renames a department and changes its order', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=edit-REST]').trigger('click')
    await w.get('input[name=edit_name]').setValue('Main restaurant')
    await w.get('input[name=edit_order]').setValue('5')
    await w.get('[data-testid=edit-form-REST]').trigger('submit')
    await flushPromises()
    expect(PATCH.mock.calls[0]?.[0]).toBe('/api/v1/properties/{propertyId}/departments/{id}')
    expect(PATCH.mock.calls[0]?.[1].params.path).toEqual({ propertyId: 7, id: 9 })
    expect(PATCH.mock.calls[0]?.[1].body).toEqual({ name: 'Main restaurant', sort_order: 5 })
    expect(w.find('[data-testid=edit-form-REST]').exists()).toBe(false)
  })

  it('switches a department off and on', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=toggle-ROOMS]').trigger('click')
    await flushPromises()
    expect(PATCH.mock.calls[0]?.[1].body).toEqual({ is_active: false })
    expect(w.get('[data-testid=notice]').text()).toContain('ROOMS is switched off')
    await w.get('[data-testid=toggle-SPA]').trigger('click')
    await flushPromises()
    expect(PATCH.mock.calls[1]?.[1].body).toEqual({ is_active: true })
  })

  it('deletes after asking', async () => {
    const w = mountView()
    await flushPromises()
    vi.spyOn(window, 'confirm').mockReturnValueOnce(false)
    await w.get('[data-testid=delete-REST]').trigger('click')
    await flushPromises()
    expect(DELETE).not.toHaveBeenCalled()
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    await w.get('[data-testid=delete-REST]').trigger('click')
    await flushPromises()
    expect(DELETE.mock.calls[0]?.[1].params.path).toEqual({ propertyId: 7, id: 9 })
    expect(w.get('[data-testid=notice]').text()).toContain('REST was deleted')
  })

  it('shows only what the role may do', async () => {
    const viewer = mountView(['accounting.view'])
    await flushPromises()
    expect(viewer.find('[data-testid=add]').exists()).toBe(false)
    expect(viewer.find('[data-testid=edit-ROOMS]').exists()).toBe(false)
    expect(viewer.find('[data-testid=toggle-ROOMS]').exists()).toBe(false)
    const none = mountView([])
    await flushPromises()
    expect(none.find('[data-testid=no-access]').exists()).toBe(true)
    expect(GET).not.toHaveBeenCalled()
  })
})
