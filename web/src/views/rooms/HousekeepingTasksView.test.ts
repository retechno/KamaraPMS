import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { setLocale } from '@/i18n'
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

  it('ticks a whole floor at once and hands it to one housekeeper', async () => {
    const data = [
      task({ id: 1, room_number: '101', floor: '1' }),
      task({ id: 2, room_id: 12, room_number: '102', floor: '1' }),
      task({ id: 3, room_id: 21, room_number: '201', floor: '2' }),
      task({ id: 4, room_id: 22, room_number: '202', floor: '2', status: 'DONE' }), // done: not part of a floor to hand out
      task({ id: 5, room_id: 101, room_number: '1001', floor: '10' }),
    ]
    const w = mountView(['housekeeping.update', 'housekeeping.assign'], data)
    await flushPromises()
    expect(w.findAll('[data-testid^=floor-]').map((b) => b.text())).toEqual(['Floor 1 (2)', 'Floor 2 (1)', 'Floor 10 (1)']) // 10 after 2
    await w.get('[data-testid=floor-1]').trigger('click')
    expect(w.get('[data-testid=floor-1]').attributes('aria-pressed')).toBe('true')
    await w.get('[data-testid=floor-2]').trigger('click')
    expect(w.get('[data-testid=assign-form]').text()).toContain('3')
    await w.get('[data-testid=floor-1]').trigger('click') // a second click on a ticked floor lets it go
    await w.get('select[name=assign_to]').setValue(10)
    POST.mockResolvedValue({ data: { assigned: 1 } })
    await w.get('[data-testid=assign-form]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls[0]?.[1].body).toEqual({ task_ids: [3], user_id: 10 })
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

  it('shows the status of a task and of its room as badges, and the kind in words', async () => {
    const w = mountView()
    await flushPromises()
    const done = w.get('[data-testid=task-103-CHECKOUT]').text()
    expect(done).toContain('Done')
    expect(done).toContain('Clean') // the room is clean
    expect(done).toContain('Check-out')
    const todo = w.get('[data-testid=task-101-ARRIVAL]').text()
    expect(todo).toContain('To do')
    expect(todo).toContain('Dirty')
    expect(todo).toContain('Arrival')
    expect(w.get('[data-testid=task-102-STAYOVER]').text()).toContain('Stayover')
  })

  it('sorts the tasks by room', async () => {
    const w = mountView()
    await flushPromises()
    const rooms = () => w.findAll('[data-testid=tasks] tbody tr').map((r) => r.findAll('td')[1]!.text().slice(0, 3))
    expect(rooms()).toEqual(['101', '102', '103'])
    await w.get('[data-testid=sort-room_number]').trigger('click')
    await w.get('[data-testid=sort-room_number]').trigger('click')
    expect(rooms()).toEqual(['103', '102', '101'])
  })

  it('speaks Indonesian, the notices included', async () => {
    setLocale('id')
    const w = mountView()
    await flushPromises()
    expect(w.get('h1').text()).toBe('Daftar pembersihan')
    expect(w.get('[data-testid=generate]').text()).toBe('Buat daftar hari ini')
    expect(w.get('[data-testid=task-101-ARRIVAL]').text()).toContain('Kedatangan')
    POST.mockResolvedValue({ data: { date: '2026-09-30', created: 4 } })
    await w.get('[data-testid=generate]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid=notice]').text()).toBe('4 tugas ditambahkan.')
    setLocale('en')
  })
})
