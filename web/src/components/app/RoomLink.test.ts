import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import RoomPanelHost from '@/components/shell/RoomPanelHost.vue'
import { closeRoomPanel, forgetRoomDirectory, useRoomPanel } from '@/composables/useRoomPanel'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import RoomLink from './RoomLink.vue'

let GET = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: vi.fn() } }))

const room = (id: number, number: string) => ({
  room_id: id, room_number: number, room_type_id: 10, room_type_code: 'DLX', room_type_name: 'Deluxe', status: 'CLEAN', status_updated_at: '2026-09-30T08:00:00Z', occupancy: 'VACANT', allowed_next: [], priority: 'NORMAL', dnd: false, make_up_requested: false,
})

function setup() {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 1, email: 'a@b.c', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions: ['reservation.read'] }] } as never
  usePropertyStore().currentId = 7
  return { pinia, router: createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] }) }
}

describe('RoomLink', () => {
  beforeEach(() => {
    setLocale('en')
    forgetRoomDirectory()
    closeRoomPanel()
    GET = vi.fn(async (path: string) => {
      if (path.endsWith('/housekeeping')) return { data: { data: [room(5, '305'), room(6, '306')] } }
      return { data: {} }
    })
  })
  afterEach(() => {
    closeRoomPanel()
    document.body.innerHTML = ''
  })

  it('shows the number, as a button that is not a <button>, and opens the panel of the room it knows by id', async () => {
    const { pinia } = setup()
    const w = mount(RoomLink, { props: { roomId: 5, number: '305' }, global: { plugins: [pinia] } })
    expect(w.text()).toBe('305')
    expect(w.attributes('role')).toBe('button')
    expect(w.attributes('tabindex')).toBe('0')
    expect(w.attributes('aria-label')).toBe('Open room 305')
    await w.trigger('click')
    await flushPromises()
    expect(useRoomPanel().target.value).toMatchObject({ roomId: 5, number: '305' })
    expect(GET).not.toHaveBeenCalled() // the id was known: nothing to look up
  })

  it('finds the room by its number when it has no id, and the list of rooms is read once', async () => {
    const { pinia } = setup()
    const a = mount(RoomLink, { props: { number: '306' }, global: { plugins: [pinia] } })
    await a.trigger('click')
    await flushPromises()
    expect(useRoomPanel().target.value).toMatchObject({ roomId: 6, number: '306' })
    const b = mount(RoomLink, { props: { number: '305' }, global: { plugins: [pinia] } })
    await b.trigger('click')
    await flushPromises()
    expect(useRoomPanel().target.value).toMatchObject({ roomId: 5 })
    expect(GET).toHaveBeenCalledTimes(1)
  })

  it('opens nothing for a number the property does not have', async () => {
    const { pinia } = setup()
    const w = mount(RoomLink, { props: { number: '999' }, global: { plugins: [pinia] } })
    await w.trigger('click')
    await flushPromises()
    expect(useRoomPanel().target.value).toBeNull()
  })

  it('does not click the row it is in', async () => {
    const { pinia } = setup()
    const rowClick = vi.fn()
    const w = mount({ components: { RoomLink }, template: '<table><tbody><tr @click="rowClick"><td><RoomLink :room-id="5" number="305" /></td></tr></tbody></table>', setup: () => ({ rowClick }) }, { global: { plugins: [pinia] } })
    await w.get('[data-slot=room-link]').trigger('click')
    expect(rowClick).not.toHaveBeenCalled()
  })

  it('opens with Enter and with Space, and the row does not hear either', async () => {
    const { pinia } = setup()
    const rowKey = vi.fn()
    const w = mount({ components: { RoomLink }, template: '<div @keydown="rowKey"><RoomLink :room-id="5" number="305" /></div>', setup: () => ({ rowKey }) }, { global: { plugins: [pinia] } })
    await w.get('[data-slot=room-link]').trigger('keydown', { key: 'Enter' })
    expect(useRoomPanel().target.value).toMatchObject({ roomId: 5 })
    closeRoomPanel()
    await w.get('[data-slot=room-link]').trigger('keydown', { key: ' ' })
    expect(useRoomPanel().target.value).toMatchObject({ roomId: 5 })
    expect(rowKey).not.toHaveBeenCalled()
  })

  it('keeps the words of the table when it has its own (a slot)', () => {
    const { pinia } = setup()
    const w = mount(RoomLink, { props: { roomId: 5, number: '305' }, slots: { default: 'Room 305' }, global: { plugins: [pinia] } })
    expect(w.text()).toBe('Room 305')
  })
})

describe('RoomPanelHost', () => {
  beforeEach(() => {
    setLocale('en')
    forgetRoomDirectory()
    closeRoomPanel()
    GET = vi.fn(async () => ({ data: {} }))
  })
  afterEach(() => {
    closeRoomPanel()
    document.body.innerHTML = ''
  })

  it('shows the panel of the room that was opened, and nothing before', async () => {
    const { pinia, router } = setup()
    const w = mount(RoomPanelHost, { attachTo: document.body, global: { plugins: [pinia, router] } })
    await flushPromises()
    expect(document.body.querySelector('[data-testid=room-sheet]')).toBeNull()
    useRoomPanel().open(5, '305')
    await flushPromises()
    expect(document.body.querySelector('[data-testid=room-sheet]')!.textContent).toContain('Room 305')
    expect(GET).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/rooms/{roomId}/summary', { params: { path: { propertyId: 7, roomId: 5 } } })
    document.body.querySelector<HTMLButtonElement>('[data-testid=rp-close]')!.click()
    await flushPromises()
    expect(useRoomPanel().target.value).toBeNull()
    expect(document.body.querySelector('[data-testid=room-sheet]')).toBeNull()
    w.unmount()
  })
})
