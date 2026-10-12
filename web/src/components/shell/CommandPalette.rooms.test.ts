import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { closeRoomPanel, forgetRoomDirectory, useRoomPanel } from '@/composables/useRoomPanel'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import CommandPalette from './CommandPalette.vue'

let GET = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a) } }))

const body = () => document.body
const $ = <T extends Element>(sel: string) => body().querySelector<T>(sel)
let mounted: VueWrapper | null = null

const room = (id: number, number: string, extra: object = {}) => ({
  room_id: id, room_number: number, room_type_id: 10, room_type_code: 'DLX', room_type_name: 'Deluxe', status: 'CLEAN', status_updated_at: '2026-09-30T08:00:00Z', occupancy: 'VACANT', allowed_next: [], priority: 'NORMAL', dnd: false, make_up_requested: false, ...extra,
})
const occupied = {
  room: room(5, '305', { occupancy: 'OCCUPIED' }), business_date: '2026-09-30', access: { stay: true, folio: true, arrival: false, audit: false },
  stay: { id: 70, stay_number: 'STY000070', guest: { id: 9, name: 'Ayu Pratiwi' }, reservation_id: 30, confirmation_number: 'RES000030', arrival_date: '2026-09-29', departure_date: '2026-10-02', night: 2, nights: 3, adult_count: 2, child_count: 0 },
  folio: { charges: '3340000', paid: '1500000', balance: '1840000', folios: [{ id: 80, folio_number: 'FOL000080', folio_type: 'GUEST', status: 'OPEN', balance: '1840000' }] },
  next_arrival: null, history: [],
}

async function openPalette(permissions = ['reservation.read', 'folio.read', 'folio.post_charge']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 1, email: 'a@b.c', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  await router.push('/')
  mounted = mount(CommandPalette, { props: { open: true, 'onUpdate:open': (v: boolean) => void mounted?.setProps({ open: v }) }, attachTo: body(), global: { plugins: [pinia, router] } })
  await flushPromises()
  return { router }
}

async function type(text: string) {
  const input = $<HTMLInputElement>('[data-testid=palette-input]')!
  input.value = text
  input.dispatchEvent(new Event('input'))
  await vi.advanceTimersByTimeAsync(250)
  await flushPromises()
}
const press = async (key: string) => {
  $('[data-testid=palette-input]')!.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true }))
  await flushPromises()
}
const rows = () => [...body().querySelectorAll('[data-testid=palette-results] [role=option]')].map((r) => r.getAttribute('data-testid'))

describe('CommandPalette: rooms', () => {
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    setLocale('en')
    forgetRoomDirectory()
    closeRoomPanel()
    GET = vi.fn(async (path: string) => {
      if (path.endsWith('/housekeeping')) return { data: { data: [room(5, '305', { occupancy: 'OCCUPIED' }), room(6, '306'), room(7, '3051'), room(8, '201')] } }
      if (path.endsWith('/rooms/{roomId}/summary')) return { data: occupied }
      return { data: { data: [] } }
    })
  })
  afterEach(() => {
    vi.useRealTimers()
    mounted?.unmount()
    mounted = null
    body().innerHTML = ''
    closeRoomPanel()
  })

  it('puts "Room 305" first for the number, with the guest and the balance, and Enter opens the panel', async () => {
    await openPalette()
    await type('305')
    expect(rows()[0]).toBe('palette-room-5')
    const first = $('[data-testid=palette-room-5]')!
    expect(first.textContent).toContain('Room 305 · DLX')
    expect(first.textContent).toContain('Ayu Pratiwi in house')
    expect(first.textContent).toMatch(/balance 1[.,]840[.,]000/)
    expect($('[data-testid=palette-room-7]')).not.toBeNull() // 3051 starts with 305, below the exact one
    expect($('[data-testid=palette-room-8]')).toBeNull()
    await press('Enter')
    expect(useRoomPanel().target.value).toMatchObject({ roomId: 5, number: '305' })
    expect($('[data-testid=command-palette]')).toBeNull() // the finder is closed
  })

  it('finds nothing for a number the property does not have', async () => {
    await openPalette()
    await type('777')
    expect(rows().some((r) => r?.startsWith('palette-room-'))).toBe(false)
  })

  it('does not read the rooms for a role that may not see them', async () => {
    await openPalette([])
    await type('305')
    expect(GET).not.toHaveBeenCalled()
    expect(rows().some((r) => r?.startsWith('palette-room-'))).toBe(false)
  })

  it('reads "+ 305" as add a charge to room 305, and goes to the charge form of the folio of the guest', async () => {
    const { router } = await openPalette()
    await type('+ 305')
    expect(rows()[0]).toBe('palette-charge-5')
    expect($('[data-testid=palette-charge-5]')!.textContent).toContain('Add a charge to room 305')
    expect(rows().some((r) => r === 'palette-room-5')).toBe(false) // the command is not a search
    await press('Enter')
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/folios/80?tab=charge')
  })

  it('offers the command only to a role that may post charges, and says why when nobody is in the room', async () => {
    await openPalette(['reservation.read'])
    await type('+305')
    expect($('[data-testid=palette-charge-5]')).toBeNull()
    mounted!.unmount()
    body().innerHTML = ''

    GET = vi.fn(async (path: string) => {
      if (path.endsWith('/housekeeping')) return { data: { data: [room(6, '306')] } }
      return { data: { ...occupied, room: room(6, '306'), stay: null, folio: null } }
    })
    forgetRoomDirectory()
    const { router } = await openPalette()
    await type('+ 306')
    const row = $('[data-testid=palette-charge-6]')!
    expect(row.getAttribute('aria-disabled')).toBe('true')
    expect(row.textContent).toContain('no guest to charge')
    await press('Enter')
    expect(router.currentRoute.value.fullPath).toBe('/')
    expect($('[data-testid=command-palette]')).not.toBeNull() // still open
  })

  it('names the rooms in Indonesian', async () => {
    setLocale('id')
    await openPalette()
    await type('305')
    expect($('[data-testid=palette-room-5]')!.textContent).toContain('Kamar 305 · DLX')
    expect($('[data-testid=palette-room-5]')!.textContent).toContain('Ayu Pratiwi menginap')
    setLocale('en')
  })
})
