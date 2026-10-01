import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import HousekeepingTasksView from './HousekeepingTasksView.vue'

let GET = vi.fn()
let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a) } }))

const task = (over: Record<string, unknown>) => ({
  id: 1, room_id: 11, room_number: '101', floor: '1', room_type_code: 'DLX', task_date: '2026-09-30', task_type: 'CHECKOUT', status: 'PENDING',
  priority: 'NORMAL', source: 'AUTO', assigned_to: null, started_at: null, completed_at: null, room_status: 'DIRTY', room_status_since: '2026-09-30T01:00:00Z',
  dnd: false, make_up_requested: false, ...over,
})
const tasks = [
  task({ id: 1, priority: 'HIGH', task_type: 'ARRIVAL' }),
  task({ id: 2, room_id: 12, room_number: '102', task_type: 'STAYOVER', assigned_to: 9, assignee_name: 'Sari', dnd: true, flag_note: 'sleeping' }),
  task({ id: 3, room_id: 13, room_number: '103', status: 'DONE', assigned_to: 9, assignee_name: 'Sari', completed_at: '2026-09-30T05:00:00Z', room_status: 'CLEAN' }),
]
const workload = [
  { user_id: 9, name: 'Sari', total: 2, pending: 1, in_progress: 0, done: 1, skipped: 0 },
  { user_id: null, name: 'Not assigned', total: 1, pending: 1, in_progress: 0, done: 0, skipped: 0 },
]
const staff = [{ id: 9, full_name: 'Sari', email: 'sari@hotel.test' }, { id: 10, full_name: 'Budi', email: 'budi@hotel.test' }]

function mountView(permissions = ['housekeeping.update', 'housekeeping.assign'], data: object[] = tasks) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 9, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn(async (path: string) => {
    if (path.endsWith('/staff')) return { data: { data: staff } }
    if (path.endsWith('/tasks')) return { data: { date: '2026-09-30', data, workload: data.length ? workload : [] } }
    return { data: { data: [{ room_id: 11, room_number: '101', room_type_code: 'DLX' }, { room_id: 12, room_number: '102', room_type_code: 'DLX' }] } }
  })
  POST = vi.fn().mockResolvedValue({ data: {} })
  return mount(HousekeepingTasksView, { global: { plugins: [pinia] } })
}

describe('HousekeepingTasksView', () => {
  beforeEach(() => {
    GET = vi.fn()
    POST = vi.fn()
  })

  it('shows the list with flags and the workload per person', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.get('[data-testid=workload-9]').text()).toContain('Sari')
    expect(w.get('[data-testid=workload-none]').text()).toContain('Not assigned')
    expect(w.get('[data-testid=task-101-ARRIVAL]').text()).toContain('High')
    expect(w.find('[data-testid=task-102-STAYOVER] [data-testid=dnd]').exists()).toBe(true)
    expect(w.get('[data-testid=task-102-STAYOVER]').text()).toContain('sleeping')
    expect(w.get('[data-testid=task-103-CHECKOUT]').classes()).toContain('closed')
    expect(w.find('[data-testid=done-103]').exists()).toBe(false) // finished
  })

  it('generates the list and says what it added', async () => {
    const w = mountView()
    await flushPromises()
    POST.mockResolvedValue({ data: { date: '2026-09-30', created: 4 } })
    await w.get('[data-testid=generate]').trigger('click')
    await flushPromises()
    expect(POST.mock.calls[0]?.[0]).toBe('/api/v1/properties/{propertyId}/housekeeping/tasks/generate')
    expect(w.get('[data-testid=notice]').text()).toContain('4 task(s) added')
  })

  it('assigns the ticked tasks to a housekeeper, or takes them back', async () => {
    const w = mountView()
    await flushPromises()
    expect((w.get('[data-testid=assign]').element as HTMLButtonElement).disabled).toBe(true)
    await w.get('[data-testid=pick-all]').setValue(true)
    await w.get('select[name=assign_to]').setValue(10)
    POST.mockResolvedValue({ data: { assigned: 2 } })
    await w.get('[data-testid=assign-form]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls[0]?.[1].body).toEqual({ task_ids: [1, 2], user_id: 10 }) // the finished task cannot be ticked
    expect(w.get('[data-testid=notice]').text()).toContain('2 task(s) assigned')
    await w.get('[data-testid=pick-all]').setValue(true)
    await w.get('select[name=assign_to]').setValue(0)
    await w.get('[data-testid=assign-form]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls[1]?.[1].body).toEqual({ task_ids: [1, 2], user_id: null })
  })

  it('starts and finishes a task and keeps a refusal visible', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=start-101]').trigger('click')
    await flushPromises()
    expect(POST.mock.calls[0]?.[0]).toBe('/api/v1/properties/{propertyId}/housekeeping/tasks/{taskId}/start')
    expect(POST.mock.calls[0]?.[1].params.path).toEqual({ propertyId: 7, taskId: 1 })
    POST.mockRejectedValue(new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'TASK_ALREADY_CLOSED', detail: 'the task is already finished' }))
    await w.get('[data-testid=done-101]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid=hk-error]').text()).toContain('TASK_ALREADY_CLOSED')
    expect(GET.mock.calls.filter(([p]) => (p as string).endsWith('/tasks')).length).toBeGreaterThan(2) // reloaded after the refusal
  })

  it('asks for a reason before skipping, prefilled for a room that must not be disturbed', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=skip-102]').trigger('click')
    expect((w.get('input[name=skip_reason]').element as HTMLInputElement).value).toBe('Do not disturb')
    await w.get('input[name=skip_reason]').setValue('guest refused')
    await w.get('[data-testid=skip-form]').trigger('submit')
    await flushPromises()
    expect(POST).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/housekeeping/tasks/{taskId}/skip', {
      params: { path: { propertyId: 7, taskId: 2 } }, body: { reason: 'guest refused' },
    })
    expect(w.find('[data-testid=skip-form]').exists()).toBe(false)
  })

  it('adds a task by hand for a chosen room', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=add-task]').trigger('click')
    await flushPromises()
    await w.get('select[name=room_id]').setValue(12)
    await w.get('select[name=assigned_to]').setValue(10)
    await w.get('input[name=notes]').setValue('turn the mattress')
    await w.get('[data-testid=task-form]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls[0]?.[1].body).toEqual({ room_id: 12, task_type: 'DEEP', priority: 'NORMAL', assigned_to: 10, notes: 'turn the mattress' })
  })

  it('shows only what the role may do', async () => {
    const maid = mountView(['housekeeping.update'])
    await flushPromises()
    expect(maid.find('[data-testid=generate]').exists()).toBe(false)
    expect(maid.find('[data-testid=assign-form]').exists()).toBe(false)
    expect(maid.find('[data-testid=pick-all]').exists()).toBe(false)
    expect(maid.find('[data-testid=start-101]').exists()).toBe(true)
    expect(GET.mock.calls.some(([p]) => (p as string).endsWith('/staff'))).toBe(false)
    const viewer = mountView([])
    await flushPromises()
    expect(viewer.find('[data-testid=start-101]').exists()).toBe(false)
    const empty = mountView(['housekeeping.assign'], [])
    await flushPromises()
    expect(empty.get('[data-testid=empty]').text()).toContain('Generate the list')
  })

  it('filters to my tasks', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('select[name=view]').setValue('mine')
    await flushPromises()
    const last = GET.mock.calls.filter(([p]) => (p as string).endsWith('/tasks')).at(-1)
    expect(last?.[1].params.query).toEqual({ assigned_to: 9 })
  })
})
