import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import HousekeepingView from './HousekeepingView.vue'

let GET = vi.fn()
let PUT = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: vi.fn(), PUT: (...a: unknown[]) => PUT(...a) } }))

const room = (over: Record<string, unknown>) => ({
  room_id: 1, room_number: '201', floor: '2', room_type_id: 1, room_type_code: 'DLX', room_type_name: 'Deluxe',
  status: 'DIRTY', status_updated_at: '2026-09-30T10:00:00Z', occupancy: 'VACANT', allowed_next: ['CLEANING', 'CLEAN'], ...over,
})
const board = [
  room({ room_id: 1, room_number: '201', floor: '2', status: 'DIRTY' }),
  room({ room_id: 2, room_number: '301', floor: '3', status: 'DIRTY' }),
  room({ room_id: 3, room_number: '302', floor: '3', status: 'DIRTY' }),
  room({ room_id: 4, room_number: '303', floor: '3', status: 'CLEAN', allowed_next: ['INSPECTED', 'DIRTY'] }),
]

async function mountAt(url: string) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions: ['housekeeping.update'] }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn().mockResolvedValue({ data: { data: board } })
  PUT = vi.fn().mockResolvedValue({ data: {} })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  await router.push(url)
  await router.isReady()
  const w = mount(HousekeepingView, { global: { plugins: [pinia, router] }, attachTo: document.body })
  await flushPromises()
  return { w, router }
}

describe('HousekeepingView: filters in the address, counts that follow them, the note sheet', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
    setLocale('en')
  })

  it('starts narrowed by ?status and ?floor, and ignores a status it does not know', async () => {
    const { w } = await mountAt('/housekeeping?status=DIRTY&floor=3')
    expect(w.find('[data-testid=room-301]').exists()).toBe(true)
    expect(w.find('[data-testid=room-302]').exists()).toBe(true)
    expect(w.find('[data-testid=room-201]').exists()).toBe(false)
    expect(w.find('[data-testid=room-303]').exists()).toBe(false)
    const other = await mountAt('/housekeeping?status=NONSENSE')
    expect(other.w.find('[data-testid=room-303]').exists()).toBe(true)
  })

  it('writes the filters it is given into the address, and takes them out when they are cleared', async () => {
    const { w, router } = await mountAt('/housekeeping')
    await w.get('[data-testid=filter-DIRTY]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query).toEqual({ status: 'DIRTY' })
    await w.get('select[name=floor]').setValue('3')
    await flushPromises()
    expect(router.currentRoute.value.query).toEqual({ status: 'DIRTY', floor: '3' })
    await w.get('[data-testid=filter-DIRTY]').trigger('click')
    await w.get('select[name=floor]').setValue('')
    await flushPromises()
    expect(router.currentRoute.value.query).toEqual({})
  })

  it('moves the filters when the address changes under the page (a link from Today)', async () => {
    const { w, router } = await mountAt('/housekeeping')
    await router.push('/housekeeping?status=CLEAN')
    await flushPromises()
    expect(w.find('[data-testid=room-303]').exists()).toBe(true)
    expect(w.find('[data-testid=room-301]').exists()).toBe(false)
  })

  it('counts on the chips what the other filters leave, with no count of the whole property', async () => {
    const { w } = await mountAt('/housekeeping')
    expect(w.get('[data-testid=filter-DIRTY]').text()).toContain('3')
    await w.get('select[name=floor]').setValue('3')
    await flushPromises()
    expect(w.get('[data-testid=filter-DIRTY]').text()).toContain('2') // floor 3 only: 301 and 302
    expect(w.get('[data-testid=filter-CLEAN]').text()).toContain('1')
  })

  it('says how many rooms the list shows', async () => {
    const { w } = await mountAt('/housekeeping')
    expect(w.get('[data-testid=result-count]').text()).toBe('4 rooms')
    await w.get('select[name=floor]').setValue('3')
    await w.get('[data-testid=filter-CLEAN]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid=result-count]').text()).toBe('1 room')
  })

  it('opens the note in a sheet with the focus in the note, and puts the focus back on the "..." of the room when it closes', async () => {
    const { w } = await mountAt('/housekeeping')
    await w.get('[data-testid=room-301] [data-slot=row-menu-trigger]').trigger('click')
    await flushPromises()
    ;(document.body.querySelector('[data-testid=flags-301]') as HTMLElement).click()
    await flushPromises()
    const sheet = document.body.querySelector('[data-testid=flag-sheet]')!
    expect(sheet.textContent).toContain('301')
    expect(document.activeElement).toBe(sheet.querySelector('input[name=note]'))
    const note = sheet.querySelector('input[name=note]') as HTMLInputElement
    note.value = 'allergic to feathers'
    note.dispatchEvent(new Event('input'))
    sheet.querySelector('[data-testid=flag-form]')!.dispatchEvent(new Event('submit', { cancelable: true }))
    await flushPromises()
    expect(PUT.mock.calls[0]?.[1]).toMatchObject({ params: { path: { propertyId: 7, id: 2 } }, body: { note: 'allergic to feathers' } })
    expect(document.body.querySelector('[data-testid=flag-sheet]')).toBeNull()
    await flushPromises()
    expect(document.activeElement).toBe(w.get('[data-testid=room-301] [data-slot=row-menu-trigger]').element)
    w.unmount()
  })
})
