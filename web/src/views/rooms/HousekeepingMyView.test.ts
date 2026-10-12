import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import HousekeepingMyView from './HousekeepingMyView.vue'

let GET = vi.fn()
let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a) } }))

const task = (over: Record<string, unknown>) => ({
  id: 1, room_id: 11, room_number: '101', floor: '1', room_type_code: 'DLX', task_date: '2026-09-30', task_type: 'CHECKOUT', status: 'PENDING',
  priority: 'NORMAL', source: 'AUTO', assigned_to: 9, assignee_name: 'Wayan', started_at: null, completed_at: null, room_status: 'DIRTY', room_status_since: '2026-09-30T01:00:00Z',
  dnd: false, make_up_requested: false, arrival_today: false, ...over,
})
// The list as the API gives it: high priority first, then floor and room. Wayan (9) has six, Komang (10) one, nobody one.
const TASKS = [
  task({ id: 1, room_id: 305, room_number: '305', floor: '3', priority: 'HIGH', arrival_today: true }),
  task({ id: 2, room_id: 304, room_number: '304', floor: '3', status: 'IN_PROGRESS', started_at: '2026-09-30T02:12:00Z', room_status: 'CLEANING' }),
  task({ id: 3, room_id: 303, room_number: '303', floor: '3', task_type: 'STAYOVER', make_up_requested: true }),
  task({ id: 4, room_id: 206, room_number: '206', floor: '2', task_type: 'STAYOVER', dnd: true, flag_note: 'sleeping' }),
  task({ id: 5, room_id: 201, room_number: '201', floor: '2', status: 'DONE', room_status: 'CLEAN' }),
  task({ id: 6, room_id: 202, room_number: '202', floor: '2', status: 'SKIPPED', notes: 'Do not disturb' }),
  task({ id: 7, room_id: 301, room_number: '301', floor: '3', assigned_to: 10, assignee_name: 'Komang' }),
  task({ id: 8, room_id: 302, room_number: '302', floor: '3', assigned_to: null, assignee_name: undefined }),
]
const WORKLOAD = [
  { user_id: 9, name: 'Wayan', total: 6, pending: 3, in_progress: 1, done: 1, skipped: 1 },
  { user_id: 10, name: 'Komang', total: 1, pending: 1, in_progress: 0, done: 0, skipped: 0 },
  { user_id: null, name: 'Not assigned', total: 1, pending: 1, in_progress: 0, done: 0, skipped: 0 },
]
const STAFF = [{ id: 9, full_name: 'Wayan', email: 'w@h.test' }, { id: 10, full_name: 'Komang', email: 'k@h.test' }]
const HOUSEKEEPER = ['housekeeping.read', 'housekeeping.update']
const SUPERVISOR = [...HOUSEKEEPER, 'housekeeping.assign']

function mountView(permissions: string[] = HOUSEKEEPER, data: object[] = TASKS) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 9, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn(async (path: string, opts?: { params?: { query?: { assigned_to?: number } } }) => {
    if (path.endsWith('/staff')) return { data: { data: STAFF } }
    const mine = opts?.params?.query?.assigned_to
    const rows = mine === undefined ? data : (data as { assigned_to: number | null }[]).filter((r) => r.assigned_to === mine)
    return { data: { date: '2026-09-30', data: rows, workload: mine === undefined ? WORKLOAD : WORKLOAD.slice(0, 1) } }
  })
  POST = vi.fn().mockResolvedValue({ data: {} })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  return mount(HousekeepingMyView, { attachTo: document.body, global: { plugins: [pinia, router] } })
}
type Wrapper = ReturnType<typeof mountView>
const rooms = (w: Wrapper) => w.findAll('[data-testid^=card-]').map((c) => c.attributes('data-testid')!.slice(5))
const body = (testid: string) => document.body.querySelector(`[data-testid=${testid}]`) as HTMLElement | null
const tasksCalls = () => GET.mock.calls.filter((c) => (c[0] as string).endsWith('/tasks'))

describe('HousekeepingMyView: the housekeeper', () => {
  beforeEach(() => {
    GET = vi.fn()
    POST = vi.fn()
  })
  afterEach(() => {
    document.body.innerHTML = ''
    vi.useRealTimers()
  })

  it('reads the tasks of the person who is signed in and shows them as cards in the order of the API', async () => {
    const w = mountView()
    await flushPromises()
    expect(tasksCalls()[0]![1]).toEqual({ params: { path: { propertyId: 7 }, query: { assigned_to: 9 } } })
    expect(rooms(w)).toEqual(['305', '304', '303', '206']) // the open ones: the next tab
    expect(w.find('[data-testid=scope]').exists()).toBe(false) // not a supervisor
    expect(w.find('[data-testid=pick-305]').exists()).toBe(false)
    expect(w.get('[data-testid=tab-next]').text()).toBe('Next · 4')
    expect(w.get('[data-testid=tab-done]').text()).toBe('Finished · 2')
    expect(w.get('[data-testid=progress]').text()).toContain('2 of 6 rooms finished')
    w.unmount()
  })

  it('shows what a card must say: priority, a guest arriving today, do not disturb, make-up, a note, since when it is cleaned', async () => {
    const w = mountView()
    await flushPromises()
    const c305 = w.get('[data-testid=card-305]')
    expect(c305.text()).toContain('Guest arrives today')
    expect(c305.text()).toContain('High')
    expect(c305.text()).toContain('DLX · floor 3 · Check-out')
    expect(w.get('[data-testid=card-206]').find('[data-testid=dnd]').exists()).toBe(true)
    expect(w.get('[data-testid=card-206]').text()).toContain('sleeping')
    expect(w.get('[data-testid=card-303]').text()).toContain('Make-up')
    expect(w.get('[data-testid=card-304]').text()).toContain('Started')
    expect(w.find('[data-testid=card-301]').exists()).toBe(false) // somebody else's task is not here
    w.unmount()
  })

  it('has Start on a pending task and Finish on a task in progress, and Skip on both', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.find('[data-testid=start-305]').exists()).toBe(true)
    expect(w.find('[data-testid=finish-305]').exists()).toBe(false)
    expect(w.find('[data-testid=start-304]').exists()).toBe(false)
    expect(w.get('[data-testid=finish-304]').text()).toBe('Finish: ready to inspect')
    expect(w.find('[data-testid=skip-305]').exists()).toBe(true)
    expect(w.find('[data-testid=skip-304]').exists()).toBe(true)
    w.unmount()
  })

  it('starts and finishes a task, and reads the list again after each', async () => {
    const w = mountView()
    await flushPromises()
    const before = tasksCalls().length
    await w.get('[data-testid=start-305]').trigger('click')
    await flushPromises()
    expect(POST.mock.calls[0]![0]).toBe('/api/v1/properties/{propertyId}/housekeeping/tasks/{taskId}/start')
    expect(POST.mock.calls[0]![1]).toEqual({ params: { path: { propertyId: 7, taskId: 1 } } })
    expect(tasksCalls().length).toBe(before + 1)
    await w.get('[data-testid=finish-304]').trigger('click')
    await flushPromises()
    expect(POST.mock.calls[1]![0]).toBe('/api/v1/properties/{propertyId}/housekeeping/tasks/{taskId}/complete')
    expect(POST.mock.calls[1]![1]).toMatchObject({ params: { path: { propertyId: 7, taskId: 2 } } })
    expect(tasksCalls().length).toBe(before + 2)
    w.unmount()
  })

  it('skips only with a reason; a room that does not want to be disturbed has it filled in', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=skip-305]').trigger('click')
    await flushPromises()
    const confirmButton = () => body('skip-confirm') as HTMLButtonElement
    expect(body('skip-sheet')!.textContent).toContain('Skip room 305')
    expect(confirmButton().disabled).toBe(true) // no reason, no skip
    const input = document.body.querySelector('[name=skip_reason]') as HTMLInputElement
    input.value = 'Guest asked to come back later'
    input.dispatchEvent(new Event('input'))
    await flushPromises()
    expect(confirmButton().disabled).toBe(false)
    confirmButton().click()
    await flushPromises()
    expect(POST.mock.calls[0]![0]).toBe('/api/v1/properties/{propertyId}/housekeeping/tasks/{taskId}/skip')
    expect(POST.mock.calls[0]![1]).toMatchObject({ params: { path: { propertyId: 7, taskId: 1 } }, body: { reason: 'Guest asked to come back later' } })
    expect(body('skip-sheet')).toBeNull() // closed

    await w.get('[data-testid=skip-206]').trigger('click') // do not disturb
    await flushPromises()
    expect((document.body.querySelector('[name=skip_reason]') as HTMLInputElement).value).toBe('Do not disturb')
    expect((body('skip-confirm') as HTMLButtonElement).disabled).toBe(false)
    w.unmount()
  })

  it('keeps the sheet open and shows the refusal when the skip is refused', async () => {
    const w = mountView()
    await flushPromises()
    const { ApiError } = await import('@/api/problem')
    POST = vi.fn().mockRejectedValue(new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'TASK_ALREADY_CLOSED', detail: 'the task is already finished' }))
    await w.get('[data-testid=skip-206]').trigger('click')
    await flushPromises()
    ;(body('skip-confirm') as HTMLButtonElement).click()
    await flushPromises()
    expect(body('skip-sheet')).not.toBeNull()
    expect(body('skip-error')).not.toBeNull()
    w.unmount()
  })

  it('narrows to a floor, and the progress stays that of the whole job', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.get('[data-testid=floors]').text()).toContain('Floor 2')
    await w.get('[data-testid=floor-2]').trigger('click')
    expect(rooms(w)).toEqual(['206'])
    expect(w.get('[data-testid=tab-next]').text()).toBe('Next · 1')
    expect(w.get('[data-testid=progress]').text()).toContain('2 of 6')
    await w.get('[data-testid=floor-all]').trigger('click')
    expect(rooms(w)).toHaveLength(4)
    w.unmount()
  })

  it('shows the finished tasks on their own tab, with the reason of a skipped one and no buttons', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=tab-done]').trigger('click')
    expect(rooms(w)).toEqual(['201', '202'])
    expect(w.get('[data-testid=card-202]').text()).toContain('Do not disturb')
    expect(w.find('[data-testid=start-202]').exists()).toBe(false)
    expect(w.find('[data-testid=skip-202]').exists()).toBe(false)
    w.unmount()
  })

  it('says so when there is nothing to do, and when a tab is empty', async () => {
    const w = mountView(HOUSEKEEPER, [])
    await flushPromises()
    expect(w.get('[data-testid=empty]').text()).toContain('no tasks for you today')
    w.unmount()
    const only = mountView(HOUSEKEEPER, [task({ id: 5, status: 'DONE' })])
    await flushPromises()
    expect(only.get('[data-testid=empty-tab]').text()).toContain('Nothing left to do.')
    only.unmount()
  })

  it('has nothing for a role that cannot do or assign cleaning', async () => {
    const w = mountView(['housekeeping.read'])
    await flushPromises()
    expect(w.find('[data-testid=no-access]').exists()).toBe(true)
    expect(tasksCalls()).toHaveLength(0)
    w.unmount()
  })

  it('speaks Indonesian', async () => {
    setLocale('id')
    const w = mountView()
    await flushPromises()
    expect(w.get('[data-testid=tab-next]').text()).toBe('Berikutnya · 4')
    expect(w.get('[data-testid=progress]').text()).toContain('2 dari 6 kamar selesai')
    expect(w.get('[data-testid=start-305]').text()).toBe('Mulai bersihkan')
    expect(w.get('[data-testid=card-305]').text()).toContain('Tamu tiba hari ini')
    w.unmount()
    setLocale('en')
  })
})

describe('HousekeepingMyView: reading again by itself', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })
  afterEach(() => {
    document.body.innerHTML = ''
    vi.useRealTimers()
    Object.defineProperty(document, 'hidden', { configurable: true, value: false })
  })

  it('reads again every minute, not while the tab is hidden, and when the tab comes back', async () => {
    const w = mountView()
    await flushPromises()
    expect(tasksCalls()).toHaveLength(1)
    await vi.advanceTimersByTimeAsync(60_000)
    expect(tasksCalls()).toHaveLength(2)

    Object.defineProperty(document, 'hidden', { configurable: true, value: true })
    await vi.advanceTimersByTimeAsync(60_000)
    expect(tasksCalls()).toHaveLength(2) // hidden: nothing

    Object.defineProperty(document, 'hidden', { configurable: true, value: false })
    document.dispatchEvent(new Event('visibilitychange'))
    await flushPromises()
    expect(tasksCalls()).toHaveLength(3)
    w.unmount()
    await vi.advanceTimersByTimeAsync(120_000)
    expect(tasksCalls()).toHaveLength(3) // the timer went with the page
  })

  it('does not read again while the skip sheet is open', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=skip-305]').trigger('click')
    await flushPromises()
    const before = tasksCalls().length
    await vi.advanceTimersByTimeAsync(180_000)
    expect(tasksCalls()).toHaveLength(before)
    ;(document.body.querySelector('[data-testid=skip-sheet]') as HTMLElement | null)?.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    w.unmount()
  })
})

describe('HousekeepingMyView: the supervisor', () => {
  beforeEach(() => {
    GET = vi.fn()
    POST = vi.fn()
  })
  afterEach(() => {
    document.body.innerHTML = ''
  })

  it('has "Mine" and "Everyone", and everyone shows the workload of each person and who has each task', async () => {
    const w = mountView(SUPERVISOR)
    await flushPromises()
    expect(w.get('[data-testid=scope-mine]').attributes('aria-selected')).toBe('true')
    expect(rooms(w)).toEqual(['305', '304', '303', '206'])
    await w.get('[data-testid=scope-all]').trigger('click')
    await flushPromises()
    expect(tasksCalls().at(-1)![1]).toEqual({ params: { path: { propertyId: 7 }, query: {} } }) // no assigned_to
    expect(w.get('[data-testid=person-9]').text()).toContain('Wayan')
    expect(w.get('[data-testid=person-9]').text()).toContain('2 of 6')
    expect(w.get('[data-testid=person-10]').text()).toContain('0 of 1')
    expect(w.get('[data-testid=person-none]').text()).toContain('Not assigned')
    expect(w.get('[data-testid=card-301]').get('[data-testid=assignee]').text()).toContain('Assigned to Komang')
    expect(w.get('[data-testid=card-302]').get('[data-testid=assignee]').text()).toContain('Not assigned')
    expect(rooms(w)).toHaveLength(6) // the open tasks of all
    w.unmount()
  })

  it('narrows to one person by touching their workload, and back by touching it again', async () => {
    const w = mountView(SUPERVISOR)
    await flushPromises()
    await w.get('[data-testid=scope-all]').trigger('click')
    await flushPromises()
    await w.get('[data-testid=person-10]').trigger('click')
    expect(rooms(w)).toEqual(['301'])
    expect(w.get('[data-testid=progress]').text()).toContain('0 of 1')
    await w.get('[data-testid=person-none]').trigger('click')
    expect(rooms(w)).toEqual(['302'])
    await w.get('[data-testid=person-none]').trigger('click')
    expect(rooms(w)).toHaveLength(6)
    w.unmount()
  })

  it('works only on their own tasks from here, and can tick the open tasks of anybody', async () => {
    const w = mountView(SUPERVISOR)
    await flushPromises()
    await w.get('[data-testid=scope-all]').trigger('click')
    await flushPromises()
    expect(w.find('[data-testid=start-305]').exists()).toBe(true) // theirs
    expect(w.find('[data-testid=start-301]').exists()).toBe(false) // Komang's
    expect(w.find('[data-testid=pick-301]').exists()).toBe(true)
    expect(w.find('[data-testid=assign-bar]').exists()).toBe(false)
    w.unmount()
  })

  it('gives the selected tasks to a person from a sheet, and takes them back', async () => {
    const w = mountView(SUPERVISOR)
    await flushPromises()
    await w.get('[data-testid=scope-all]').trigger('click')
    await flushPromises()
    await w.get('[data-testid=pick-302]').setValue(true)
    await w.get('[data-testid=pick-301]').setValue(true)
    expect(w.get('[data-testid=picked-count]').text()).toBe('2 selected')
    await w.get('[data-testid=assign-open]').trigger('click')
    await flushPromises()
    expect(body('assign-sheet')!.textContent).toContain('Wayan')
    ;(body('assign-to-9') as HTMLButtonElement).click()
    await flushPromises()
    expect(POST.mock.calls[0]![0]).toBe('/api/v1/properties/{propertyId}/housekeeping/tasks/assign')
    expect(POST.mock.calls[0]![1]).toMatchObject({ body: { task_ids: expect.arrayContaining([7, 8]), user_id: 9 } })
    expect((POST.mock.calls[0]![1] as { body: { task_ids: number[] } }).body.task_ids).toHaveLength(2)
    expect(w.get('[data-testid=notice]').text()).toBe('2 tasks assigned.') // the mock answers {}: the number is the one asked
    expect(w.find('[data-testid=assign-bar]').exists()).toBe(false)
    expect(body('assign-sheet')).toBeNull()

    await w.get('[data-testid=pick-302]').setValue(true)
    await w.get('[data-testid=assign-open]').trigger('click')
    await flushPromises()
    ;(body('assign-take-back') as HTMLButtonElement).click()
    await flushPromises()
    expect(POST.mock.calls[1]![1]).toMatchObject({ body: { task_ids: [8], user_id: null } })
    expect(w.get('[data-testid=notice]').text()).toBe('1 task taken back.')
    w.unmount()
  })

  it('a supervisor who does no cleaning starts at everyone and has no buttons of the work', async () => {
    const w = mountView(['housekeeping.read', 'housekeeping.assign'])
    await flushPromises()
    expect(w.find('[data-testid=scope]').exists()).toBe(false)
    expect(tasksCalls()[0]![1]).toEqual({ params: { path: { propertyId: 7 }, query: {} } })
    expect(w.find('[data-testid=workload]').exists()).toBe(true)
    expect(w.find('[data-testid=start-305]').exists()).toBe(false)
    expect(w.find('[data-testid=open-full]').exists()).toBe(true)
    w.unmount()
  })
})
