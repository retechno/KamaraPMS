import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import HomeView from './HomeView.vue'

// A fresh mock per test (reassigned in beforeEach); the module mock delegates to it.
let GET = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...args: unknown[]) => GET(...args) } }))

const PATH = '/api/v1/properties/{propertyId}'

// ---- the system status (a card for the administrator)
const healthOnly = async (path: string) => ({ data: { status: 'ok', path } })

function mountPlain(admin = true) {
  const pinia = createPinia()
  setActivePinia(pinia)
  // The status of the system is for the administrator.
  useAuthStore().me = { user: { id: 5, email: 'a@hotel.com', is_tenant_admin: admin }, properties: [] } as never
  return mount(HomeView, { global: { plugins: [pinia] } })
}

describe('HomeView system status', () => {
  beforeEach(() => {
    GET = vi.fn()
    setLocale('en')
  })

  it('shows no system status, and makes no health check, to staff who are not administrators', async () => {
    GET.mockImplementation(healthOnly)
    const wrapper = mountPlain(false)
    await flushPromises()
    expect(wrapper.find('[data-testid=system-status-card]').exists()).toBe(false)
    expect(GET).not.toHaveBeenCalledWith('/healthz')
  })

  it('shows API and database as operational', async () => {
    GET.mockImplementation(healthOnly)
    const wrapper = mountPlain()
    await flushPromises()
    expect(GET).toHaveBeenCalledWith('/healthz')
    expect(GET).toHaveBeenCalledWith('/readyz')
    expect(wrapper.get('[data-testid=api-status]').text()).toBe('Operational')
    expect(wrapper.get('[data-testid=db-status]').text()).toBe('Operational')
    expect(wrapper.find('[role=alert]').exists()).toBe(false)
  })

  it('reports a database outage with its error code', async () => {
    GET.mockImplementation(async (path: string) => {
      if (path === '/healthz') return { data: { status: 'ok' } }
      throw new ApiError({ type: 't', title: 'Service Unavailable', status: 503, code: 'NOT_READY', detail: 'the database is not reachable', request_id: 'req-1' })
    })
    const wrapper = mountPlain()
    await flushPromises()
    expect(wrapper.get('[data-testid=api-status]').text()).toBe('Operational')
    expect(wrapper.get('[data-testid=db-status]').text()).toBe('Unavailable')
    expect(wrapper.get('[role=alert]').text()).toContain('NOT_READY')
    expect(wrapper.get('[role=alert]').text()).toContain('req-1')
  })

  it('reports an unreachable API without guessing the database state', async () => {
    GET.mockRejectedValue(new TypeError('Failed to fetch'))
    const wrapper = mountPlain()
    await flushPromises()
    expect(wrapper.get('[data-testid=api-status]').text()).toBe('Unavailable')
    expect(wrapper.get('[data-testid=db-status]').text()).toBe('Unknown')
    expect(wrapper.get('[role=alert]').text()).toContain('NETWORK_ERROR')
  })
})

// ---- the page of the day
const night = (i: number, held: number, sellable = 100) => ({
  date: `2026-10-${String(10 + i).padStart(2, '0')}`, sellable, blocked: 0, held, available: sellable - held, occupancy_percent: ((held / sellable) * 100).toFixed(2), in_house: held, arrivals: 0, reservations: 0, complimentary: 0, house_use: 0,
})
const NIGHTS = Array.from({ length: 14 }, (_, i) => night(i, i === 0 ? 78 : 40 + i * 4))

const arrival = (id: number, over: Record<string, unknown> = {}) => ({
  reservation_id: id, confirmation_number: `RES00000${id}`, reservation_room_id: id, reservation_version: 1, guest_id: id, guest_name: `Guest ${id}`, room_type_id: 1, room_type_code: 'DLX', room_type_name: 'Deluxe',
  room_id: 50 + id, room_number: `${200 + id}`, housekeeping_status: 'CLEAN', arrival_date: '2026-10-10', departure_date: '2026-10-12', adult_count: 2, child_count: 0, status: 'CONFIRMED', reservation_status: 'CONFIRMED',
  rate: { rate_plan_code: 'BAR', rate_plan_name: 'Best', amount: '900000', price_mode: 'NET' }, company: null, deposit: null, readiness: { status: 'READY', blockers: [] }, ...over,
})
const stay = (id: number, balance = '150000', status = 'OUTSTANDING') => ({
  id, stay_number: `STY00000${id}`, version: 1, reservation_id: id, confirmation_number: `RES00000${id}`, guest: { id, name: `Leaver ${id}` }, room: { id: 60 + id, number: `${300 + id}`, room_type_code: 'STD', room_type_name: 'Standard' },
  company: null, billing: [], rate: { rate_plan_code: 'BAR', rate_plan_name: 'Best', amount: '1', price_mode: 'NET', is_override: false }, stay: { arrival_date: '2026-10-08', departure_date: '2026-10-10', nights: 2, adults: 2, children: 0 },
  balance: { amount: balance, status, folios: [] }, checkout: { status: 'READY', uncharged_nights: 0 },
})
const board = (n: number, over: Record<string, unknown> = {}) => ({ room_id: n, room_number: `${100 + n}`, room_type_id: 1, room_type_code: 'STD', room_type_name: 'Standard', status: 'CLEAN', status_updated_at: '', occupancy: 'VACANT', allowed_next: [], priority: 'NORMAL', dnd: false, make_up_requested: false, ...over })

interface World {
  arrivals?: unknown[]
  checkedIn?: unknown[]
  departures?: unknown[]
  rooms?: unknown[]
  accounts?: unknown[]
  nights?: unknown[]
  failing?: string[]
}
const defaultWorld = (): World => ({
  arrivals: [
    arrival(1),
    arrival(2, { housekeeping_status: 'DIRTY', readiness: { status: 'BLOCKED', blockers: ['ROOM_NOT_READY'] } }),
    arrival(3, { room_id: null, room_number: '', housekeeping_status: undefined, room_type_code: 'FAM', readiness: { status: 'BLOCKED', blockers: ['ROOM_NOT_ASSIGNED'] } }),
  ],
  checkedIn: [arrival(8, { status: 'CHECKED_IN' })],
  departures: [stay(1), stay(2, '-500000', 'CREDIT'), stay(3, '', 'NO_FOLIO')],
  rooms: [board(1), board(2, { status: 'INSPECTED' }), board(3, { status: 'DIRTY' }), board(4, { status: 'DIRTY' }), board(5, { status: 'CLEANING' }), board(6, { occupancy: 'OCCUPIED' })],
  accounts: [{ company_id: 1, code: 'ACME', name: 'Acme Corp', credit_limit: '1000000', available: '-250000' }, { company_id: 2, code: 'B', name: 'Beta', credit_limit: null, available: null }],
  nights: NIGHTS,
})
let world = defaultWorld()

function serve(): void {
  GET = vi.fn(async (path: string, init?: { params?: { query?: Record<string, unknown> } }) => {
    const fail = (key: string) => {
      if (world.failing?.includes(key)) throw new ApiError({ type: 't', title: 'x', status: 500, code: 'INTERNAL', detail: 'boom' })
    }
    switch (path) {
      case `${PATH}/availability/calendar`:
        fail('calendar')
        return { data: { from: '2026-10-10', to: '2026-10-24', room_types: [], totals: world.nights } }
      case `${PATH}/arrivals`:
        fail('arrivals')
        return { data: { data: init?.params?.query?.status === 'CHECKED_IN' ? world.checkedIn : world.arrivals } }
      case `${PATH}/stays/in-house`:
        fail('departures')
        return { data: { data: world.departures } }
      case `${PATH}/housekeeping`:
        fail('rooms')
        return { data: { data: world.rooms } }
      case `${PATH}/city-ledger/accounts`:
        fail('accounts')
        return { data: { data: world.accounts } }
      case `${PATH}/stays/{id}`:
        fail('stay')
        return { data: { stay: { id: 1, status: 'OPEN', departure_date: '2026-10-10', version: 1 }, folios: [], guest: { id: 1 } } }
      default:
        return { data: { status: 'ok' } }
    }
  })
}

const FRONT = ['reservation.read', 'housekeeping.read', 'frontdesk.checkin', 'frontdesk.checkout', 'reservation.create', 'cityledger.read', 'nightaudit.run']
let mounted: VueWrapper | null = null
let router: ReturnType<typeof createRouter>

async function mountToday(permissions: string[] = FRONT, clock: object | null = undefined as never, admin = false) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 1, email: 'a@b.c', full_name: 'Khalil Ahmad', is_tenant_admin: admin }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  const property = usePropertyStore()
  property.loaded = true
  property.properties = [{ id: 7, code: 'BALI', name: 'Bali' }] as never
  property.currentId = 7
  property.current = { id: 7, name: 'Bali Resort', night_audit_earliest_time: '23:00' } as never
  property.clock = (clock === undefined ? { business_date: '2026-10-10', property_local_time: '2026-10-10T09:05:00+08:00', timezone: 'Asia/Makassar', night_audit_allowed: false, night_audit_overdue: false } : clock) as never
  router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  await router.push('/')
  mounted = mount(HomeView, {
    attachTo: document.body,
    global: { plugins: [pinia, router], stubs: {
      CheckInPanel: { props: ['arrival'], emits: ['done', 'cancel'], template: `<div><span data-testid="checkin-panel">{{ arrival.guest_name }}</span><button data-testid="stub-done" @click="$emit('done', { stay: { stay_number: 'STY1' }, stay_room: { room_number: '201' } })" /><button data-testid="stub-cancel" @click="$emit('cancel')" /></div>` },
      CheckOutWizard: { props: ['detail'], emits: ['done', 'cancel'], template: `<div><span data-testid="checkout-wizard">{{ detail.stay.id }}</span><button data-testid="stub-done" @click="$emit('done', { posted_room_charges: [] })" /></div>` },
    } },
  })
  await flushPromises()
  return mounted
}

const callsTo = (path: string) => GET.mock.calls.filter((c) => c[0] === path).length
const bodyEl = (sel: string) => document.body.querySelector(sel)

describe('HomeView, Today', () => {
  beforeEach(() => {
    setLocale('en')
    world = defaultWorld()
    serve()
  })
  afterEach(() => {
    mounted?.unmount()
    mounted = null
    document.body.innerHTML = ''
    vi.useRealTimers()
  })

  describe('the strip', () => {
    it('shows tonight\'s occupancy with its meter, the arrivals left of all, the departures left, and the rooms ready to sell, without any money', async () => {
      const w = await mountToday()
      const strip = w.get('[data-testid=strip]')
      expect(strip.get('[data-testid=strip-occupancy]').text()).toContain('78.00%')
      expect(strip.get('[data-testid=strip-occupancy]').text()).toContain('78 / 100 rooms')
      expect(strip.get('[data-testid=strip-meter]').attributes('style')).toContain('width: 78%')
      expect(strip.get('[data-testid=strip-arrivals]').text()).toContain('3 left') // like the departures: "n left"
      expect(strip.get('[data-testid=strip-arrivals]').text()).toContain('1 of 4 checked in') // 3 to come, 1 came
      expect(strip.get('[data-testid=strip-departures]').text()).toContain('3 left')
      expect(strip.get('[data-testid=strip-ready]').text()).toContain('2') // two clean or inspected rooms that are empty
      expect(strip.get('[data-testid=strip-ready]').text()).toContain('2 dirty · 1 being cleaned')
      expect(strip.text()).not.toMatch(/IDR|Rp|\d[.,]\d{3}/) // no figure of money
      expect(GET.mock.calls.some((c) => String(c[0]).endsWith('/dashboard'))).toBe(false) // the dashboard is for the managers
    })

    it('is as wide as the columns: four equal cards on a wide screen, two by two on a phone, and as many as there are for a role that reads less', async () => {
      const w = await mountToday()
      const strip = w.get('[data-testid=strip]')
      expect(strip.classes()).toContain('grid-cols-2') // a phone: two by two
      expect(strip.classes().join(' ')).toContain('lg:grid-cols-[repeat(var(--cards),minmax(0,1fr))]') // wide: the cards share the whole row
      expect(strip.attributes('style')).toContain('--cards: 4')
      expect(strip.findAll('[data-slot=kpi-card]')).toHaveLength(4)
      mounted?.unmount()
      const housekeeper = await mountToday(['housekeeping.read'])
      expect(housekeeper.get('[data-testid=strip]').attributes('style')).toContain('--cards: 1')
    })

    it('shows only what a person may read: a housekeeper sees the rooms and not the movements', async () => {
      const w = await mountToday(['housekeeping.read'])
      expect(w.find('[data-testid=strip-ready]').exists()).toBe(true)
      expect(w.find('[data-testid=strip-arrivals]').exists()).toBe(false)
      expect(w.find('[data-testid=col-arrivals]').exists()).toBe(false)
      expect(callsTo(`${PATH}/arrivals`)).toBe(0)
      expect(callsTo(`${PATH}/city-ledger/accounts`)).toBe(0)
    })

    it('shows nothing and asks nothing of a person who may read none of it', async () => {
      const w = await mountToday(['guest.read'])
      expect(w.find('[data-testid=strip]').exists()).toBe(false)
      expect(GET.mock.calls.filter((c) => String(c[0]).startsWith(PATH))).toHaveLength(0)
    })
  })

  describe('the header', () => {
    it('greets by the hour of the property and by first name, and sums up the day', async () => {
      const w = await mountToday()
      const text = w.get('[data-slot=page-header]').text()
      expect(w.get('h1').text()).toBe('Today')
      expect(text).toContain('Good morning, Khalil.')
      expect(text).toContain('3 guests still to arrive, 3 still to leave, 2 rooms still dirty.')
    })

    it('says "evening" late in the day, and speaks Indonesian', async () => {
      const w = await mountToday(FRONT, { business_date: '2026-10-10', property_local_time: '2026-10-10T17:30:00+08:00', timezone: 'Asia/Makassar', night_audit_allowed: false, night_audit_overdue: false })
      expect(w.get('[data-slot=page-header]').text()).toContain('Good evening, Khalil.')
      mounted?.unmount()
      setLocale('id')
      const id = await mountToday()
      expect(id.get('h1').text()).toBe('Hari Ini')
      expect(id.get('[data-slot=page-header]').text()).toContain('Selamat pagi, Khalil.')
      expect(id.get('[data-slot=page-header]').text()).toContain('3 tamu masih akan tiba')
    })

    it('offers the quick actions the role may use', async () => {
      const all = await mountToday()
      expect(all.get('[data-testid=quick-new-reservation]').attributes('href')).toBe('/reservations/new')
      expect(all.get('[data-testid=quick-walk-in]').attributes('href')).toBe('/walk-in')
      mounted?.unmount()
      const none = await mountToday(['guest.read'])
      expect(none.find('[data-testid=quick-new-reservation]').exists()).toBe(false)
      expect(none.find('[data-testid=quick-walk-in]').exists()).toBe(false)
    })

    it('shows the property', async () => {
      const w = await mountToday()
      expect(w.get('[data-testid=property-name]').text()).toBe('Bali Resort')
    })
  })

  describe('the arrivals column', () => {
    it('has the guest, the room and type, a chip for the room, and a Check in button for who may', async () => {
      const w = await mountToday()
      const col = w.get('[data-testid=col-arrivals]')
      expect(col.get('[data-testid=col-arrivals-count]').text()).toContain('3 left')
      expect(col.get('[data-testid=arrival-1]').text()).toContain('Guest 1')
      expect(col.get('[data-testid=arrival-1]').text()).toContain('201 · DLX')
      expect(col.get('[data-testid=arrival-chip-1]').text()).toBe('Clean') // the colour and word of the housekeeping status
      expect(col.get('[data-testid=arrival-chip-2]').text()).toBe('Dirty')
      expect(col.get('[data-testid=arrival-chip-3]').text()).toBe('No room yet')
      // the main button only for the arrival that is ready; the others are outline buttons that still open the panel
      expect(col.get('[data-testid=check-in-1]').attributes('data-ready')).toBe('true')
      expect(col.get('[data-testid=check-in-1]').classes().join(' ')).toContain('bg-primary')
      expect(col.get('[data-testid=check-in-2]').attributes('data-ready')).toBe('false')
      expect(col.get('[data-testid=check-in-2]').classes().join(' ')).not.toContain('bg-primary')
      expect(col.get('[data-testid=check-in-3]').classes().join(' ')).toContain('border-border')
      expect(col.find('[data-testid=check-in-1]').exists()).toBe(true)
      mounted?.unmount()
      const reader = await mountToday(['reservation.read'])
      expect(reader.find('[data-testid=check-in-1]').exists()).toBe(false)
      expect(reader.find('[data-testid=arrival-1]').exists()).toBe(true)
    })

    describe('the chip and the button are what the server says about readiness', () => {
      const ready = (over: Record<string, unknown> = {}) => ({ readiness: { status: 'READY', blockers: [] }, ...over })
      const blocked = (blockers: string[], over: Record<string, unknown> = {}) => ({ readiness: { status: 'BLOCKED', blockers }, ...over })
      const chipOf = async (a: unknown) => {
        world.arrivals = [a]
        const w = await mountToday()
        const chip = w.get('[data-testid=arrival-chip-1]')
        const button = w.get('[data-testid=check-in-1]')
        mounted?.unmount()
        return { text: chip.text(), kind: chip.attributes('data-chip'), classes: chip.classes().join(' '), ready: button.attributes('data-ready'), solid: button.classes().join(' ').includes('bg-primary') }
      }

      it('shows "Clean" and a green Check in for a room that is ready', async () => {
        expect(await chipOf(arrival(1, ready()))).toMatchObject({ text: 'Clean', kind: 'housekeeping', ready: 'true', solid: true })
        expect(await chipOf(arrival(1, ready({ housekeeping_status: 'INSPECTED' })))).toMatchObject({ text: 'Inspected', kind: 'housekeeping', solid: true })
      })

      it('shows "No room yet" in outline for ROOM_NOT_ASSIGNED, and Check in is not the main button', async () => {
        const r = await chipOf(arrival(1, blocked(['ROOM_NOT_ASSIGNED'], { room_id: null, room_number: '', housekeeping_status: undefined })))
        expect(r).toMatchObject({ text: 'No room yet', kind: 'noRoom', ready: 'false', solid: false })
        expect(r.classes).toContain('border-border')
      })

      it('shows "Waiting for check-out" in the warning colour for ROOM_OCCUPIED, even when the room is clean', async () => {
        const r = await chipOf(arrival(1, blocked(['ROOM_OCCUPIED'], { housekeeping_status: 'CLEAN' })))
        expect(r).toMatchObject({ text: 'Waiting for check-out', kind: 'waitingCheckOut', ready: 'false', solid: false })
        expect(r.classes).toContain('bg-warning/20')
      })

      it('shows "Blocked" in the closed status colour for ROOM_BLOCKED and for ROOM_NOT_AVAILABLE', async () => {
        for (const blocker of ['ROOM_BLOCKED', 'ROOM_NOT_AVAILABLE']) {
          const r = await chipOf(arrival(1, blocked([blocker])))
          expect(r, blocker).toMatchObject({ text: 'Blocked', kind: 'blocked', solid: false })
          expect(r.classes, blocker).toContain('status-closed')
        }
      })

      it('shows the housekeeping status of the room for ROOM_NOT_READY: "Dirty" or "Being cleaned"', async () => {
        expect(await chipOf(arrival(1, blocked(['ROOM_NOT_READY'], { housekeeping_status: 'DIRTY' })))).toMatchObject({ text: 'Dirty', kind: 'housekeeping', solid: false })
        expect(await chipOf(arrival(1, blocked(['ROOM_NOT_READY'], { housekeeping_status: 'CLEANING' })))).toMatchObject({ text: 'Being cleaned', kind: 'housekeeping', solid: false })
      })

      it('shows "Guest details incomplete" for GUEST_MISSING', async () => {
        expect(await chipOf(arrival(1, blocked(['GUEST_MISSING'])))).toMatchObject({ text: 'Guest details incomplete', kind: 'guestMissing', ready: 'false', solid: false })
      })

      it('takes the first blocker that matches when a room has several: occupied before not ready', async () => {
        expect(await chipOf(arrival(1, blocked(['ROOM_NOT_READY', 'ROOM_OCCUPIED'], { housekeeping_status: 'DIRTY' })))).toMatchObject({ text: 'Waiting for check-out' })
        expect(await chipOf(arrival(1, blocked(['GUEST_MISSING', 'ROOM_NOT_READY'], { housekeeping_status: 'DIRTY' })))).toMatchObject({ text: 'Dirty' })
      })

      it('still opens the check-in panel from an arrival that is not ready, to choose a room', async () => {
        world.arrivals = [arrival(1, blocked(['ROOM_OCCUPIED']))]
        const w = await mountToday()
        await w.get('[data-testid=check-in-1]').trigger('click')
        await flushPromises()
        expect(bodyEl('[data-testid=checkin-panel]')).not.toBeNull()
      })

      it('says the words in Indonesian', async () => {
        setLocale('id')
        expect((await chipOf(arrival(1, blocked(['ROOM_OCCUPIED'])))).text).toBe('Menunggu check-out')
        expect((await chipOf(arrival(1, blocked(['ROOM_BLOCKED'])))).text).toBe('Diblokir')
        expect((await chipOf(arrival(1, blocked(['GUEST_MISSING'])))).text).toBe('Data tamu belum lengkap')
        expect((await chipOf(arrival(1, blocked(['ROOM_NOT_ASSIGNED'], { room_id: null, housekeeping_status: undefined })))).text).toBe('Belum ada kamar')
      })
    })

    it('shows five rows at most and a link to all of them', async () => {
      world.arrivals = Array.from({ length: 8 }, (_, i) => arrival(i + 1))
      const w = await mountToday()
      expect(w.get('[data-testid=col-arrivals]').findAll('li')).toHaveLength(5)
      expect(w.get('[data-testid=col-arrivals-count]').text()).toContain('8 left') // the count is of all of them
      expect(w.get('[data-testid=col-arrivals-all]').attributes('href')).toBe('/arrivals')
      expect(w.get('[data-testid=col-arrivals-all]').text()).toBe('All')
    })

    it('says so when nobody is left to arrive', async () => {
      world.arrivals = []
      const w = await mountToday()
      expect(w.get('[data-testid=col-arrivals-empty]').text()).toContain('No one is left to arrive today.')
    })

    it('opens the check-in that the front desk has, in a sheet, for that arrival', async () => {
      const w = await mountToday()
      expect(bodyEl('[data-testid=checkin-sheet]')).toBeNull()
      await w.get('[data-testid=check-in-2]').trigger('click')
      await flushPromises()
      const sheet = bodyEl('[data-testid=checkin-sheet]')!
      expect(sheet).not.toBeNull()
      expect(sheet.textContent).toContain('Guest 2')
      expect(bodyEl('[data-testid=checkin-panel]')?.textContent).toBe('Guest 2')
    })

    it('reads the day again after a check-in, and closes the sheet', async () => {
      const w = await mountToday()
      await w.get('[data-testid=check-in-1]').trigger('click')
      await flushPromises()
      const before = callsTo(`${PATH}/arrivals`)
      ;(bodyEl('[data-testid=stub-done]') as HTMLElement).click()
      await flushPromises()
      expect(bodyEl('[data-testid=checkin-sheet]')).toBeNull()
      expect(callsTo(`${PATH}/arrivals`)).toBeGreaterThan(before)
    })
  })

  describe('the departures column', () => {
    it('has the guest, the room, the balance as the folios state it (a credit in words), and a Check out button for who may', async () => {
      const w = await mountToday()
      const col = w.get('[data-testid=col-departures]')
      expect(col.get('[data-testid=departure-1]').text()).toContain('Leaver 1')
      expect(col.get('[data-testid=departure-1]').text()).toContain('301 · STD')
      expect(col.get('[data-testid=departure-balance-1]').text()).toContain('150,000')
      expect(col.get('[data-testid=departure-balance-2]').text()).toContain('500,000 credit')
      expect(col.get('[data-testid=departure-balance-3]').text()).toContain('No folio')
      // what is still owed is in the warning colour and "to pay"; a credit and no folio are neutral
      expect(col.get('[data-testid=departure-balance-1]').attributes('data-owes')).toBe('true')
      expect(col.get('[data-testid=departure-balance-1]').classes()).toContain('text-warning-text')
      expect(col.get('[data-testid=departure-balance-1]').text()).toContain('to pay')
      expect(col.get('[data-testid=departure-balance-2]').attributes('data-owes')).toBe('false')
      expect(col.get('[data-testid=departure-balance-2]').classes()).not.toContain('text-warning-text')
      expect(col.get('[data-testid=departure-balance-2]').text()).toContain('balance')
      expect(col.get('[data-testid=departure-balance-2]').text()).not.toContain('to pay')
      expect(col.find('[data-testid=check-out-1]').exists()).toBe(true)
      expect(col.get('[data-testid=col-departures-all]').attributes('href')).toBe('/departures')
      mounted?.unmount()
      const reader = await mountToday(['reservation.read'])
      expect(reader.find('[data-testid=check-out-1]').exists()).toBe(false)
    })

    it('shows five rows at most', async () => {
      world.departures = Array.from({ length: 7 }, (_, i) => stay(i + 1))
      const w = await mountToday()
      expect(w.get('[data-testid=col-departures]').findAll('li')).toHaveLength(5)
      expect(w.get('[data-testid=col-departures-count]').text()).toContain('7 left')
    })

    it('opens the check-out wizard that the stay page has, in a sheet, with the stay it read', async () => {
      const w = await mountToday()
      await w.get('[data-testid=check-out-2]').trigger('click')
      await flushPromises()
      expect(GET).toHaveBeenCalledWith(`${PATH}/stays/{id}`, { params: { path: { propertyId: 7, id: 2 } } })
      expect(bodyEl('[data-testid=checkout-sheet]')?.textContent).toContain('Leaver 2')
      expect(bodyEl('[data-testid=checkout-wizard]')).not.toBeNull()
    })

    it('reads the day again after a check-out', async () => {
      const w = await mountToday()
      await w.get('[data-testid=check-out-1]').trigger('click')
      await flushPromises()
      const before = callsTo(`${PATH}/stays/in-house`)
      ;(bodyEl('[data-testid=stub-done]') as HTMLElement).click()
      await flushPromises()
      expect(bodyEl('[data-testid=checkout-sheet]')).toBeNull()
      expect(callsTo(`${PATH}/stays/in-house`)).toBeGreaterThan(before)
    })
  })

  describe('the column of what needs attention', () => {
    it('counts the rooms that are not ready for the arrivals, the reservations without a room, and the accounts over their limit', async () => {
      const w = await mountToday()
      expect(w.get('[data-testid=attention-unready]').text()).toContain('1 room is not ready for today\'s arrivals')
      expect(w.get('[data-testid=attention-unready]').text()).toContain('202 · assign to housekeeping')
      expect(w.get('[data-testid=attention-no-room]').text()).toContain('1 reservation has no room yet')
      expect(w.get('[data-testid=attention-no-room]').text()).toContain('Arriving today · FAM')
      expect(w.get('[data-testid=attention-over-limit]').text()).toContain('1 company account is over its credit limit')
      expect(w.get('[data-testid=attention-over-limit]').text()).toContain('Acme Corp')
      expect(w.get('[data-testid=attention-unready] a').attributes('href')).toBe('/housekeeping')
    })

    it('counts a clean room that a guest has not left as "occupied", not as ready, in a row of its own', async () => {
      world.arrivals = [
        arrival(1),
        arrival(2, { housekeeping_status: 'CLEAN', readiness: { status: 'BLOCKED', blockers: ['ROOM_OCCUPIED'] } }),
        arrival(3, { housekeeping_status: 'CLEAN', readiness: { status: 'BLOCKED', blockers: ['ROOM_OCCUPIED'] } }),
        arrival(4, { housekeeping_status: 'DIRTY', readiness: { status: 'BLOCKED', blockers: ['ROOM_NOT_READY'] } }),
        arrival(5, { readiness: { status: 'BLOCKED', blockers: ['ROOM_BLOCKED'] } }),
      ]
      const w = await mountToday()
      expect(w.get('[data-testid=attention-occupied]').text()).toContain('2 rooms are still occupied by guests who have not checked out')
      expect(w.get('[data-testid=attention-occupied]').text()).toContain('202, 203 · waiting for check-out')
      expect(w.get('[data-testid=attention-occupied] a').attributes('href')).toBe('/departures')
      expect(w.get('[data-testid=attention-unready]').text()).toContain('1 room is not ready') // only the room that is dirty
      expect(w.get('[data-testid=attention-unready]').text()).toContain('204')
      expect(w.get('[data-testid=attention-blocked]').text()).toContain('1 room for an arrival is blocked')
      // the night audit keeps its row when there are more items than rows
      expect(w.get('[data-testid=col-attention]').findAll('li').length).toBeLessThanOrEqual(5)
      expect(w.find('[data-testid=attention-audit]').exists()).toBe(true)
    })

    it('keeps the night audit among the five rows when every other item is there', async () => {
      world.arrivals = [
        arrival(1, { readiness: { status: 'BLOCKED', blockers: ['ROOM_NOT_READY'] } }),
        arrival(2, { readiness: { status: 'BLOCKED', blockers: ['ROOM_OCCUPIED'] } }),
        arrival(3, { room_id: null, readiness: { status: 'BLOCKED', blockers: ['ROOM_NOT_ASSIGNED'] } }),
        arrival(4, { readiness: { status: 'BLOCKED', blockers: ['ROOM_BLOCKED'] } }),
      ]
      const w = await mountToday()
      const ids = w.get('[data-testid=col-attention]').findAll('li').map((li) => li.attributes('data-testid'))
      expect(ids).toHaveLength(5)
      expect(ids.at(-1)).toBe('attention-audit')
      expect(ids).toEqual(['attention-unready', 'attention-occupied', 'attention-no-room', 'attention-blocked', 'attention-audit']) // the room items first; the accounts do not fit
    })

    it('has no item for what is fine, and no accounts for a person who may not read them', async () => {
      world.arrivals = [arrival(1)]
      world.accounts = []
      const w = await mountToday()
      expect(w.find('[data-testid=attention-unready]').exists()).toBe(false)
      expect(w.find('[data-testid=attention-no-room]').exists()).toBe(false)
      expect(w.find('[data-testid=attention-over-limit]').exists()).toBe(false)
      expect(w.find('[data-testid=attention-audit]').exists()).toBe(true) // the night audit is always told
      mounted?.unmount()
      world = defaultWorld()
      GET.mockClear()
      const noLedger = await mountToday(['reservation.read', 'housekeeping.read'])
      expect(noLedger.find('[data-testid=attention-over-limit]').exists()).toBe(false)
      expect(callsTo(`${PATH}/city-ledger/accounts`)).toBe(0)
    })

    it('tells the state of the night audit: when it opens, that it can run, that it is overdue, with a link only for who runs it', async () => {
      const opens = await mountToday()
      expect(opens.get('[data-testid=attention-audit]').text()).toContain('Night audit opens at 23:00')
      expect(opens.get('[data-testid=attention-audit] a').attributes('href')).toBe('/night-audit')
      mounted?.unmount()
      const ready = await mountToday(FRONT, { business_date: '2026-10-10', property_local_time: '2026-10-10T23:30:00+08:00', timezone: 'Asia/Makassar', night_audit_allowed: true, night_audit_overdue: false })
      expect(ready.get('[data-testid=attention-audit]').text()).toContain('Night audit can run now')
      mounted?.unmount()
      const late = await mountToday(['reservation.read'], { business_date: '2026-10-10', property_local_time: '2026-10-11T08:00:00+08:00', timezone: 'Asia/Makassar', night_audit_allowed: true, night_audit_overdue: true })
      expect(late.get('[data-testid=attention-audit]').text()).toContain('Night audit is overdue')
      expect(late.find('[data-testid=attention-audit] a').exists()).toBe(false)
    })

    it('shows five items at most', async () => {
      const w = await mountToday()
      expect(w.get('[data-testid=col-attention]').findAll('li').length).toBeLessThanOrEqual(5)
    })
  })

  describe('the nights ahead', () => {
    it('draws fourteen bars from the calendar, with the lines at 50% and 100%', async () => {
      const w = await mountToday()
      const chart = w.get('[data-testid=chart]')
      expect(chart.findAll('[data-testid=night-bar]')).toHaveLength(14)
      expect(chart.get('[data-testid=night-bar]').attributes('style')).toContain('height: 78%')
      expect(chart.find('[data-slot=line-50]').exists()).toBe(true)
      expect(chart.find('[data-slot=line-100]').exists()).toBe(true)
      expect(chart.text()).toContain('Next 14 nights')
      expect(GET).toHaveBeenCalledWith(`${PATH}/availability/calendar`, { params: { path: { propertyId: 7 }, query: { from: '2026-10-10', to: '2026-10-24' } } })
    })
  })

  describe('a part that fails', () => {
    it('is marked in its own place and the rest of the page is shown', async () => {
      world.failing = ['arrivals']
      const w = await mountToday()
      expect(w.get('[data-testid=col-arrivals-failed]').text()).toContain('could not be loaded')
      expect(w.find('[data-testid=col-arrivals] li').exists()).toBe(false)
      // the others are up
      expect(w.find('[data-testid=col-departures] li').exists()).toBe(true)
      expect(w.get('[data-testid=strip-ready]').text()).toContain('2')
      expect(w.get('[data-testid=chart]').findAll('[data-testid=night-bar]')).toHaveLength(14)
      expect(w.get('[data-testid=strip-arrivals]').text()).toContain('–')
    })

    it('keeps what it had when a later read fails, and says so', async () => {
      const w = await mountToday()
      expect(w.findAll('[data-testid=col-departures] li')).toHaveLength(3)
      world.failing = ['departures']
      await w.get('[data-testid=refresh]').trigger('click')
      await flushPromises()
      expect(w.find('[data-testid=col-departures-failed]').exists()).toBe(true)
      expect(w.findAll('[data-testid=col-departures] li')).toHaveLength(3)
      world.failing = []
      await w.get('[data-testid=refresh]').trigger('click')
      await flushPromises()
      expect(w.find('[data-testid=col-departures-failed]').exists()).toBe(false)
    })

    it('marks the chart when the calendar fails', async () => {
      world.failing = ['calendar']
      const w = await mountToday()
      expect(w.find('[data-testid=chart-failed]').exists()).toBe(true)
      expect(w.get('[data-testid=strip-occupancy]').text()).toContain('–')
    })
  })

  describe('reading again by itself', () => {
    beforeEach(() => {
      vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval', 'Date'] })
      vi.setSystemTime(new Date('2026-10-10T01:05:00Z')) // 09:05 in Makassar
    })

    it('reads again every two minutes, and not before', async () => {
      await mountToday()
      const first = callsTo(`${PATH}/arrivals`)
      vi.advanceTimersByTime(119_000)
      await flushPromises()
      expect(callsTo(`${PATH}/arrivals`)).toBe(first)
      vi.advanceTimersByTime(1_000)
      await flushPromises()
      expect(callsTo(`${PATH}/arrivals`)).toBe(first + 2) // the two reads of the arrivals
      vi.advanceTimersByTime(120_000)
      await flushPromises()
      expect(callsTo(`${PATH}/arrivals`)).toBe(first + 4)
    })

    it('reads again when the tab comes back, and not while it is hidden', async () => {
      await mountToday()
      const first = callsTo(`${PATH}/stays/in-house`)
      Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' })
      Object.defineProperty(document, 'hidden', { configurable: true, value: true })
      vi.advanceTimersByTime(120_000)
      await flushPromises()
      expect(callsTo(`${PATH}/stays/in-house`)).toBe(first) // nobody is looking: no read
      Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
      Object.defineProperty(document, 'hidden', { configurable: true, value: false })
      document.dispatchEvent(new Event('visibilitychange'))
      await flushPromises()
      expect(callsTo(`${PATH}/stays/in-house`)).toBe(first + 1)
    })

    it('does not read again while the check-in sheet is open, and does when it is closed', async () => {
      const w = await mountToday()
      await w.get('[data-testid=check-in-1]').trigger('click')
      await flushPromises()
      const open = callsTo(`${PATH}/arrivals`)
      vi.advanceTimersByTime(240_000)
      document.dispatchEvent(new Event('visibilitychange'))
      await flushPromises()
      expect(callsTo(`${PATH}/arrivals`)).toBe(open)
      ;(bodyEl('[data-testid=stub-cancel]') as HTMLElement).click()
      await flushPromises()
      vi.advanceTimersByTime(120_000)
      await flushPromises()
      expect(callsTo(`${PATH}/arrivals`)).toBe(open + 2)
    })

    it('does not read again while the check-out sheet is open', async () => {
      const w = await mountToday()
      await w.get('[data-testid=check-out-1]').trigger('click')
      await flushPromises()
      const open = callsTo(`${PATH}/stays/in-house`)
      vi.advanceTimersByTime(240_000)
      await flushPromises()
      expect(callsTo(`${PATH}/stays/in-house`)).toBe(open)
    })

    it('stops when the page is left', async () => {
      const w = await mountToday()
      const first = callsTo(`${PATH}/arrivals`)
      w.unmount()
      mounted = null
      vi.advanceTimersByTime(600_000)
      await flushPromises()
      expect(callsTo(`${PATH}/arrivals`)).toBe(first)
    })

    it('has a Refresh button and says when it was updated, in the zone of the property', async () => {
      const w = await mountToday()
      expect(w.get('[data-testid=updated]').text()).toBe('Updated 09:05')
      vi.setSystemTime(new Date('2026-10-10T01:09:00Z'))
      const first = callsTo(`${PATH}/arrivals`)
      await w.get('[data-testid=refresh]').trigger('click')
      await flushPromises()
      expect(callsTo(`${PATH}/arrivals`)).toBe(first + 2)
      expect(w.get('[data-testid=updated]').text()).toBe('Updated 09:09')
      mounted?.unmount()
      setLocale('id')
      const id = await mountToday()
      expect(id.get('[data-testid=updated]').text()).toBe('Diperbarui 09.09')
    })

    it('shows what was read for the property that is open when a slow answer for the one before comes last', async () => {
      const w = await mountToday()
      let slow: (v: unknown) => void = () => undefined
      const original = GET.getMockImplementation()!
      GET.mockImplementationOnce(() => new Promise((resolve) => { slow = resolve })) // the calendar of the refresh: slow
      void w.get('[data-testid=refresh]').trigger('click')
      await flushPromises()
      // another property is opened (its nights are different) while the answer is on its way
      world.nights = NIGHTS.map((n, i) => (i === 0 ? { ...n, held: 10, occupancy_percent: '10.00' } : n))
      GET.mockImplementation(original)
      const auth = useAuthStore()
      auth.me = { ...auth.me, properties: [...auth.me!.properties, { id: 8, code: 'UBUD', name: 'Ubud', permissions: FRONT }] } as never
      usePropertyStore().currentId = 8
      await flushPromises()
      expect(w.get('[data-testid=strip-occupancy]').text()).toContain('10.00%')
      slow({ data: { totals: NIGHTS } }) // the older answer, with the nights of the other property, comes last
      await flushPromises()
      expect(w.get('[data-testid=strip-occupancy]').text()).toContain('10.00%')
    })
  })
})
