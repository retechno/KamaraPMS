import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { DEFAULT_PINS, RECENT_LIMIT, resetNavPins, useNavPins } from './useNavPins'

// One page of the menu that is for some roles only, as the manager's performance page will be: the filter by permission is proved on it.
vi.mock('@/navigation', async (orig) => {
  const m = await orig<typeof import('@/navigation')>()
  m.navigation[0]!.items.push({ id: 'performance', to: '/performance', permission: 'report.view' })
  return m
})

let wrapper: VueWrapper | null = null

async function setup(opts: { userId?: number; admin?: boolean; permissions?: string[] } = {}) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = {
    user: { id: opts.userId ?? 1, email: 'a@b.c', is_tenant_admin: opts.admin ?? false },
    properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions: opts.permissions ?? [] }],
  } as never
  usePropertyStore().currentId = 7
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  await router.push('/')
  let api!: ReturnType<typeof useNavPins>
  wrapper = mount(
    defineComponent({
      setup() {
        api = useNavPins()
        return () => null
      },
    }),
    { global: { plugins: [pinia, router] } },
  )
  await flushPromises()
  return api
}

const ids = (list: { item: { id: string } }[]) => list.map((x) => x.item.id)

describe('useNavPins', () => {
  beforeEach(() => {
    localStorage.clear()
    resetNavPins()
  })
  afterEach(() => {
    wrapper?.unmount()
    wrapper = null
    vi.restoreAllMocks()
  })

  describe('what a new person gets', () => {
    it('is the dashboard, room status, tape chart and cashier, those they may open', async () => {
      expect(DEFAULT_PINS.map((d) => d.id)).toEqual(['dashboard', 'roomStatus', 'tapeChart', 'cashier'])
      const all = await setup({ permissions: ['reservation.read', 'folio.read'] })
      expect(ids(all.pinned.value)).toEqual(['dashboard', 'roomStatus', 'tapeChart', 'cashier'])
      wrapper!.unmount()
      resetNavPins()
      const reception = await setup({ permissions: ['reservation.read'] })
      expect(ids(reception.pinned.value)).toEqual(['dashboard', 'roomStatus', 'tapeChart'])
      wrapper!.unmount()
      resetNavPins()
      const nobody = await setup({ permissions: [] })
      expect(ids(nobody.pinned.value)).toEqual(['dashboard'])
    })

    it('stores nothing until the person pins or unpins something', async () => {
      await setup({ permissions: ['reservation.read'] })
      expect(localStorage.length).toBe(0)
    })
  })

  describe('pinning', () => {
    it('adds and removes a page, keeps the order, and remembers it for this person only', async () => {
      const pins = await setup({ userId: 1, permissions: ['reservation.read'] })
      pins.togglePin('guests')
      expect(ids(pins.pinned.value)).toEqual(['dashboard', 'roomStatus', 'tapeChart', 'guests'])
      expect(pins.isPinned('guests')).toBe(true)
      pins.togglePin('roomStatus')
      expect(ids(pins.pinned.value)).toEqual(['dashboard', 'tapeChart', 'guests'])
      expect(JSON.parse(localStorage.getItem('pms.nav.1')!).pins).toEqual(['dashboard', 'tapeChart', 'guests'])
      expect(localStorage.getItem('pms.nav.2')).toBeNull()
      pins.togglePin('guests')
      expect(pins.isPinned('guests')).toBe(false)

      // another person on the same computer starts from the defaults
      wrapper!.unmount()
      const other = await setup({ userId: 2, permissions: ['reservation.read'] })
      expect(ids(other.pinned.value)).toEqual(['dashboard', 'roomStatus', 'tapeChart'])
    })

    it('reads what was stored, in a new session', async () => {
      localStorage.setItem('pms.nav.1', JSON.stringify({ pins: ['guests', 'dashboard'], recent: ['groups'], showRecent: false }))
      const pins = await setup()
      expect(ids(pins.pinned.value)).toEqual(['guests', 'dashboard'])
      expect(ids(pins.recent.value)).toEqual(['groups'])
      expect(pins.showRecent.value).toBe(false)
    })
  })

  describe('the pages that are not shown', () => {
    it('leaves out a page that is not in the menu any more, without an error', async () => {
      localStorage.setItem('pms.nav.1', JSON.stringify({ pins: ['gone', 'dashboard', 'alsoGone'], recent: ['gone', 'guests'] }))
      const pins = await setup()
      expect(ids(pins.pinned.value)).toEqual(['dashboard'])
      expect(ids(pins.recent.value)).toEqual(['guests'])
    })

    it('leaves out a page for administrators from those who are not, and shows it to an administrator', async () => {
      localStorage.setItem('pms.nav.1', JSON.stringify({ pins: ['users', 'dashboard'], recent: ['roles', 'guests'] }))
      const user = await setup({ admin: false })
      expect(ids(user.pinned.value)).toEqual(['dashboard'])
      expect(ids(user.recent.value)).toEqual(['guests'])
      wrapper!.unmount()
      resetNavPins()
      const admin = await setup({ admin: true })
      expect(ids(admin.pinned.value)).toEqual(['users', 'dashboard'])
      expect(ids(admin.recent.value)).toEqual(['roles', 'guests'])
    })

    it('leaves out a page whose permission the person does not have, and brings it back when they have it', async () => {
      localStorage.setItem('pms.nav.1', JSON.stringify({ pins: ['performance', 'dashboard'], recent: ['performance'] }))
      const without = await setup({ permissions: [] })
      expect(ids(without.pinned.value)).toEqual(['dashboard'])
      expect(ids(without.recent.value)).toEqual([])
      wrapper!.unmount()
      resetNavPins()
      const manager = await setup({ permissions: ['report.view'] })
      expect(ids(manager.pinned.value)).toEqual(['performance', 'dashboard'])
      expect(ids(manager.recent.value)).toEqual(['performance'])
    })

    it('does not track a page that is not in the menu', async () => {
      const pins = await setup()
      pins.track('/stays/3')
      pins.track('/performance') // a page for some roles only: not for this person
      expect(ids(pins.recent.value)).toEqual([])
      expect(localStorage.length).toBe(0)
    })
  })

  describe('the pages opened last', () => {
    it('puts the page the person is on first, once, and keeps five', async () => {
      const pins = await setup({ permissions: ['reservation.read'] })
      for (const path of ['/guests', '/groups', '/reservations', '/arrivals', '/departures', '/in-house']) pins.track(path)
      expect(ids(pins.recent.value)).toEqual(['inHouse', 'departures', 'arrivals', 'reservations', 'groups'])
      expect(pins.recent.value).toHaveLength(RECENT_LIMIT)
      pins.track('/groups')
      expect(ids(pins.recent.value)).toEqual(['groups', 'inHouse', 'departures', 'arrivals', 'reservations'])
      pins.track('/groups')
      expect(ids(pins.recent.value)[0]).toBe('groups')
      expect(ids(pins.recent.value).filter((x) => x === 'groups')).toHaveLength(1)
    })

    it('counts a page below a page of the menu as that page', async () => {
      const pins = await setup()
      pins.track('/reservations/12')
      expect(ids(pins.recent.value)).toEqual(['reservations'])
    })

    it('can be hidden, and shown again', async () => {
      const pins = await setup()
      expect(pins.showRecent.value).toBe(true)
      pins.setShowRecent(false)
      expect(pins.showRecent.value).toBe(false)
      expect(JSON.parse(localStorage.getItem('pms.nav.1')!).showRecent).toBe(false)
      pins.setShowRecent(true)
      expect(pins.showRecent.value).toBe(true)
    })
  })

  describe('a browser that cannot store', () => {
    it('keeps the choice until the page is closed when the storage throws, and ignores a stored value that is not ours', async () => {
      localStorage.setItem('pms.nav.1', '{ not json')
      const pins = await setup({ permissions: [] })
      expect(ids(pins.pinned.value)).toEqual(['dashboard'])
      vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
        throw new Error('full')
      })
      pins.togglePin('guests')
      expect(ids(pins.pinned.value)).toEqual(['dashboard', 'guests'])
    })

    it('ignores a stored value of the wrong shape', async () => {
      localStorage.setItem('pms.nav.1', JSON.stringify({ pins: 'dashboard', recent: [1, 2], showRecent: 'yes' }))
      const pins = await setup({ permissions: [] })
      expect(ids(pins.pinned.value)).toEqual(['dashboard'])
      expect(ids(pins.recent.value)).toEqual([])
      expect(pins.showRecent.value).toBe(true)
    })
  })
})
