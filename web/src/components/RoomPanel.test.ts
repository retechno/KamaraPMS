import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import RoomPanel from './RoomPanel.vue'

let GET = vi.fn()
let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a) } }))

const body = () => document.body
const $ = <T extends Element>(sel: string) => body().querySelector<T>(sel)
const text = (sel: string) => $(sel)?.textContent?.replace(/\s+/g, ' ').trim() ?? ''

const board = (extra: object = {}) => ({
  room_id: 5, room_number: '305', floor: '3', building: 'North', room_type_id: 10, room_type_code: 'DLX', room_type_name: 'Deluxe', bed_type_code: 'KING', status: 'DIRTY',
  status_updated_at: '2026-09-30T08:00:00Z', occupancy: 'OCCUPIED', allowed_next: ['CLEANING', 'CLEAN'], priority: 'NORMAL', dnd: false, make_up_requested: false, ...extra,
})
const stay = (extra: object = {}) => ({
  id: 70, stay_number: 'STY000070', guest: { id: 9, name: 'Ayu Pratiwi' }, reservation_id: 30, confirmation_number: 'RES000030', source: 'OTA', arrival_date: '2026-09-29', departure_date: '2026-10-02',
  night: 2, nights: 3, adult_count: 2, child_count: 0, special_request: 'high floor, peanut allergy', ...extra,
})
const folio = (extra: object = {}) => ({
  charges: '3340000', paid: '1500000', balance: '1840000', folios: [{ id: 80, folio_number: 'FOL000080', folio_type: 'GUEST', status: 'OPEN', balance: '1840000' }], ...extra,
})
const summary = (extra: object = {}) => ({
  room: board(), business_date: '2026-09-30', access: { stay: true, folio: true, arrival: false, audit: true }, stay: stay(), folio: folio(), next_arrival: null,
  history: [
    { at: '2026-09-30T02:42:00Z', kind: 'CLEANING', from: 'CLEAN', to: 'DIRTY', notes: 'guest asked for make-up' },
    { at: '2026-09-30T01:10:00Z', kind: 'AUDIT', action: 'stay.checked_in', entity_type: 'stay', label: 'STY000070', user: 'Wayan' },
  ],
  ...extra,
})

const ALL = ['reservation.read', 'folio.read', 'payment.post', 'folio.post_charge', 'reservation.update', 'frontdesk.room_move', 'frontdesk.checkout', 'housekeeping.update', 'housekeeping.assign', 'housekeeping.inspect']

let mounted: VueWrapper | null = null
let answer: object = summary()

async function openPanel(permissions: string[] = ALL, props: Record<string, unknown> = {}) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 1, email: 'a@b.c', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  await router.push('/room-status')
  const onClose = vi.fn()
  const onChanged = vi.fn()
  mounted = mount(RoomPanel, { props: { roomId: 5, open: true, onClose, onChanged, ...props }, attachTo: body(), global: { plugins: [pinia, router] } })
  await flushPromises()
  return { router, onClose, onChanged }
}

describe('RoomPanel', () => {
  beforeEach(() => {
    setLocale('en')
    answer = summary()
    GET = vi.fn(async (path: string) => {
      if (path.endsWith('/summary')) return { data: answer }
      if (path.endsWith('/housekeeping/staff')) return { data: { data: [{ id: 4, full_name: 'Komang' }] } }
      return { data: { data: [] } }
    })
    POST = vi.fn(async () => ({ data: {} }))
  })
  afterEach(() => {
    mounted?.unmount()
    mounted = null
    body().innerHTML = ''
  })

  it('asks for the summary of its room and tells who is in it, with the night, the request and the folio', async () => {
    await openPanel()
    expect(GET).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/rooms/{roomId}/summary', { params: { path: { propertyId: 7, roomId: 5 } } })
    expect(text('[data-testid=rp-number]')).toContain('305')
    expect(text('[data-testid=room-sheet]')).toContain('Room 305')
    expect(text('[data-testid=rp-guest]')).toBe('Ayu Pratiwi')
    expect(text('[data-testid=rp-stay-line]')).toBe('29 Sep 2026 → 2 Oct 2026 · night 2 of 3 · 2 adults · OTA')
    expect(text('[data-testid=rp-request]')).toContain('peanut allergy')
    expect(text('[data-testid=rp-charges]')).toMatch(/^3[.,]340[.,]000$/)
    expect($('[data-testid=rp-owes-chip]')).not.toBeNull()
    expect($('[data-owes=true]')!.textContent).toContain('To pay')
    expect(text('[data-testid=rp-chips]')).toContain('Occupied')
  })

  it('says nobody is in an empty room and who comes next', async () => {
    answer = summary({
      room: board({ occupancy: 'VACANT', allowed_next: [] }), stay: null, folio: null, access: { stay: true, folio: true, arrival: true, audit: true },
      next_arrival: { reservation_id: 31, confirmation_number: 'RES000031', guest_name: 'Lucas Martin', room_type_code: 'DLX', arrival_date: '2026-10-14', departure_date: '2026-10-16', nights: 2, adult_count: 2, child_count: 0 },
    })
    await openPanel()
    expect($('[data-testid=rp-stay]')).toBeNull()
    expect($('[data-testid=rp-folio]')).toBeNull()
    expect($('[data-testid=rp-empty]')).not.toBeNull()
    expect(text('[data-testid=rp-next-arrival]')).toContain('Lucas Martin')
    expect(text('[data-testid=rp-next-arrival]')).toContain('Arrives 14 Oct 2026 · 2 nights')
    expect($<HTMLAnchorElement>('[data-testid=rp-reservation]')!.getAttribute('href')).toBe('/reservations/31')
  })

  it('leaves out what the role may not read and says so', async () => {
    answer = summary({ stay: null, folio: null, access: { stay: false, folio: false, arrival: false, audit: false } })
    await openPanel(['housekeeping.update'])
    expect($('[data-testid=rp-stay]')).toBeNull()
    expect($('[data-testid=rp-empty]')).toBeNull() // "nobody is in this room" would be a lie: the role cannot tell
    expect($('[data-testid=rp-folio]')).toBeNull()
    expect($('[data-testid=rp-hidden]')).not.toBeNull()
  })

  it('shows a stay without a folio as that, and a credit or a settled folio without the warning colour', async () => {
    answer = summary({ folio: null })
    await openPanel()
    expect($('[data-testid=rp-no-folio]')).not.toBeNull()
    mounted!.unmount()
    answer = summary({ folio: folio({ balance: '0', paid: '3340000' }) })
    await openPanel()
    expect($('[data-owes=false]')).not.toBeNull()
    expect($('[data-testid=rp-owes-chip]')).toBeNull()
  })

  it('offers only the actions the role may do, each a link to the flow that exists', async () => {
    await openPanel()
    const href = (id: string) => $<HTMLAnchorElement>(`[data-testid=rp-action-${id}]`)?.getAttribute('href')
    expect(href('pay')).toBe('/folios/80?tab=payment')
    expect(href('charge')).toBe('/folios/80?tab=charge')
    expect(href('extend')).toBe('/stays/70?action=extend')
    expect(href('move')).toBe('/stays/70?action=move')
    expect(href('checkout')).toBe('/stays/70?action=checkout')
    expect($('[data-testid=rp-action-housekeeping]')).not.toBeNull()
    expect(body().querySelectorAll('[data-testid^=rp-action-]').length).toBeLessThanOrEqual(6)
    mounted!.unmount()

    await openPanel(['reservation.read', 'folio.read', 'payment.post'])
    expect($('[data-testid=rp-action-pay]')).not.toBeNull()
    for (const id of ['charge', 'extend', 'move', 'checkout', 'housekeeping']) expect($(`[data-testid=rp-action-${id}]`)).toBeNull()
  })

  it('has no payment or charge action once the folio is closed, and none for a room without a guest', async () => {
    answer = summary({ folio: folio({ folios: [{ id: 80, folio_number: 'FOL000080', folio_type: 'GUEST', status: 'CLOSED', balance: '0' }] }) })
    await openPanel()
    expect($('[data-testid=rp-action-pay]')).toBeNull()
    expect($('[data-testid=rp-action-charge]')).toBeNull()
    mounted!.unmount()
    answer = summary({ room: board({ occupancy: 'VACANT' }), stay: null, folio: null })
    await openPanel()
    for (const id of ['pay', 'charge', 'extend', 'move', 'checkout']) expect($(`[data-testid=rp-action-${id}]`)).toBeNull()
    expect($('[data-testid=rp-action-housekeeping]')).not.toBeNull() // a room can be sent to housekeeping whoever is in it
  })

  it('closes and goes to the page when an action link is followed', async () => {
    const { router, onClose } = await openPanel()
    $<HTMLAnchorElement>('[data-testid=rp-action-checkout]')!.click()
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/stays/70?action=checkout')
    expect(onClose).toHaveBeenCalled()
  })

  it('has the keys P, T and O for pay, charge and check-out, except while typing', async () => {
    const { router } = await openPanel()
    const key = async (k: string, target: Element = $('[data-testid=room-sheet]')!) => {
      target.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true }))
      await flushPromises()
    }
    await key('t')
    expect(router.currentRoute.value.fullPath).toBe('/folios/80?tab=charge')
    mounted!.unmount()

    const second = await openPanel()
    await key('O')
    expect(second.router.currentRoute.value.fullPath).toBe('/stays/70?action=checkout')
    mounted!.unmount()

    const third = await openPanel()
    const input = document.createElement('input')
    $('[data-testid=room-sheet]')!.appendChild(input)
    await key('p', input)
    expect(third.router.currentRoute.value.fullPath).toBe('/room-status') // typing a p is not a command
    expect(text('[data-testid=room-sheet]')).toContain('pay') // the footer names the keys
  })

  it('changes the cleaning with the buttons for the next statuses, then reads the room again', async () => {
    const { onChanged } = await openPanel()
    $<HTMLButtonElement>('[data-testid=rp-mark-CLEANING]')!.click()
    await flushPromises()
    expect(POST).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/rooms/{id}/housekeeping', { params: { path: { propertyId: 7, id: 5 } }, body: { status: 'CLEANING' } })
    expect(GET.mock.calls.filter((c) => String(c[0]).endsWith('/summary'))).toHaveLength(2)
    expect(onChanged).toHaveBeenCalled()
  })

  it('shows the next statuses as plain chips to a role that may not change the cleaning, and INSPECTED only to an inspector', async () => {
    await openPanel(['reservation.read'])
    expect($('[data-testid=rp-mark-CLEANING]')).toBeNull()
    expect(text('[data-testid=rp-cleaning]')).toContain('Being cleaned')
    mounted!.unmount()
    answer = summary({ room: board({ status: 'CLEAN', allowed_next: ['INSPECTED', 'DIRTY'] }) })
    await openPanel(['housekeeping.update'])
    expect($('[data-testid=rp-mark-DIRTY]')).not.toBeNull()
    expect($('[data-testid=rp-mark-INSPECTED]')).toBeNull()
  })

  it('sends housekeeping to the room with a small form, then reads the room again', async () => {
    const { onChanged } = await openPanel()
    $<HTMLButtonElement>('[data-testid=rp-action-housekeeping]')!.click()
    await flushPromises()
    expect($('[data-testid=rp-hk-form]')).not.toBeNull()
    expect(GET).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/housekeeping/staff', { params: { path: { propertyId: 7 } } })
    expect($<HTMLSelectElement>('select[name=task_type]')!.value).toBe('STAYOVER') // somebody is in the room
    const set = async (sel: string, value: string) => {
      const el = $<HTMLSelectElement | HTMLInputElement>(sel)!
      el.value = value
      el.dispatchEvent(new Event(el.tagName === 'SELECT' ? 'change' : 'input'))
      await flushPromises()
    }
    await set('select[name=priority]', 'HIGH')
    await set('select[name=assigned_to]', '4')
    await set('input[name=notes]', 'make-up please')
    $<HTMLFormElement>('[data-testid=rp-hk-form]')!.dispatchEvent(new Event('submit'))
    await flushPromises()
    expect(POST).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/housekeeping/tasks', {
      params: { path: { propertyId: 7 } }, body: { room_id: 5, task_type: 'STAYOVER', priority: 'HIGH', assigned_to: 4, notes: 'make-up please' },
    })
    expect($('[data-testid=rp-hk-form]')).toBeNull()
    expect(onChanged).toHaveBeenCalled()
  })

  it('says the floor once, in the header, and hides the key hints on a touch screen and on a phone', async () => {
    await openPanel()
    expect((text('[data-testid=room-sheet]').match(/Floor/g) ?? []).length).toBe(1)
    expect(text('[data-testid=rp-place]')).toBe('Floor 3 · North · KING')
    const hints = [...body().querySelectorAll('[data-testid=rp-key-hint]')]
    expect(hints.length).toBe(3) // P, T and O
    for (const h of hints) expect(h.className).toContain('max-sm:hidden pointer-coarse:hidden')
  })

  it('lists what happened today, the change of cleaning and the audit entries', async () => {
    await openPanel()
    const items = [...body().querySelectorAll('[data-testid=rp-history] li')].map((li) => li.textContent!.replace(/\s+/g, ' ').trim())
    expect(items).toHaveLength(2)
    expect(items[0]).toContain('Cleaning: Clean → Dirty · guest asked for make-up')
    expect(items[1]).toContain('Wayan')
    expect(items[1]).toContain('STY000070')
    mounted!.unmount()
    answer = summary({ history: [] })
    await openPanel()
    expect($('[data-testid=rp-history-empty]')).not.toBeNull()
  })

  it('draws the room it was given at once and keeps it when the summary cannot be read', async () => {
    GET = vi.fn(async () => {
      throw new Error('offline')
    })
    await openPanel(ALL, { seed: board({ flag_note: 'Leak in the bathroom', block: { type: 'OOO', end_date: '2026-10-03' } }) })
    expect(text('[data-testid=room-sheet]')).toContain('Room 305')
    expect(text('[data-testid=room-sheet]')).toContain('Leak in the bathroom')
    expect(text('[data-testid=sheet-block]')).toContain('3 Oct 2026')
    expect($('[data-testid=rp-stay]')).toBeNull()
  })

  it('speaks Indonesian', async () => {
    setLocale('id')
    await openPanel()
    expect(text('[data-testid=rp-stay-line]')).toContain('malam ke-2 dari 3')
    expect(text('[data-testid=rp-actions]')).toContain('Terima pembayaran')
    expect(text('[data-testid=rp-actions]')).toContain('Kirim housekeeping')
    expect($('[data-owes=true]')!.textContent).toContain('Harus dibayar')
    setLocale('en')
  })
})
