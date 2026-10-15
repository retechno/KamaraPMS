import { describe, expect, it } from 'vitest'
import { i18n } from '@/i18n'
import { router } from '@/router'
import { activeNav, findNavItem, navigation, permitted, visibleNavigation } from './navigation'

const items = navigation.flatMap((s) => s.items)

describe('navigation', () => {
  it('points every item at a real page', () => {
    for (const item of items) {
      const resolved = router.resolve(item.to)
      expect(resolved.name, item.to).not.toBe('not-found')
      expect(resolved.matched.length, item.to).toBeGreaterThan(0)
    }
  })

  it('has no duplicate id or path', () => {
    expect(new Set(items.map((i) => i.id)).size).toBe(items.length)
    expect(new Set(items.map((i) => i.to)).size).toBe(items.length)
  })

  it('has a label in every language for every section and item', () => {
    const messages = i18n.global.messages.value as unknown as Record<string, { nav: { sections: object; items: object } }>
    for (const locale of ['en', 'id']) {
      const nav = messages[locale]!.nav
      for (const s of navigation) expect(Object.keys(nav.sections), `${locale} section ${s.id}`).toContain(s.id)
      for (const i of items) {
        expect(Object.keys(nav.items), `${locale} item ${i.id}`).toContain(i.id)
      }
    }
  })

  it('keeps administrator pages from everyone else', () => {
    const asUser = visibleNavigation(false).flatMap((s) => s.items.map((i) => i.id))
    const asAdmin = visibleNavigation(true).flatMap((s) => s.items.map((i) => i.id))
    for (const id of ['properties', 'users', 'roles']) {
      expect(asUser).not.toContain(id)
      expect(asAdmin).toContain(id)
    }
    // the pages of setup are for who manages what they show (docs/architecture/33-setup-read-permissions.md): nothing of setup without a permission
    expect(asUser).not.toContain('roomTypes')
  })

  describe('the section of setup (docs/architecture/33-setup-read-permissions.md)', () => {
    const sectionsOf = (held: string[]) => visibleNavigation(false, (p) => held.includes(p)).map((s) => s.id)
    const setupOf = (held: string[]) => visibleNavigation(false, (p) => held.includes(p)).find((s) => s.id === 'setup')?.items.map((i) => i.id) ?? []

    it('is not shown to housekeeping or to the reception, whatever they read', () => {
      expect(sectionsOf(['housekeeping.update', 'housekeeping.assign', 'maintenance.report'])).not.toContain('setup')
      const reception = ['reservation.read', 'reservation.create', 'reservation.update', 'frontdesk.checkin', 'frontdesk.checkout', 'folio.read', 'payment.post', 'guest.read', 'guest.write', 'cityledger.read']
      expect(sectionsOf(reception)).not.toContain('setup')
      expect(visibleNavigation(false).map((s) => s.id)).not.toContain('setup')
    })

    it('shows each page to the permission that manages what it shows', () => {
      expect(setupOf(['room.manage'])).toEqual(['roomTypes', 'bedTypes', 'roomsSetup'])
      expect(setupOf(['billing_config.manage'])).toEqual(['taxesService', 'chargeCodes'])
      expect(setupOf(['rate.manage'])).toEqual(['ratePlans', 'freeQuotas', 'rateGrid', 'bedSupplements', 'restrictions', 'yieldRules'])
      expect(setupOf(['property.manage'])).toEqual(['approvalRules'])
      expect(setupOf(['company.manage'])).toEqual(['companies'])
      expect(setupOf(['audit.read'])).toEqual(['auditTrail'])
    })

    it('names a manage permission on every page that is not for administrators only', () => {
      const setup = navigation.find((s) => s.id === 'setup')!.items
      for (const i of setup.filter((x) => !x.adminOnly && x.id !== 'auditTrail')) expect(String([i.permission]), i.id).toMatch(/\.manage$/)
    })

    it('has the permission of the menu on the route of the page, so that a typed address is turned back', () => {
      const setup = navigation.find((s) => s.id === 'setup')!.items.filter((i) => i.permission && !i.adminOnly && i.to.startsWith('/setup/'))
      for (const i of setup) {
        const route = router.resolve(i.to).matched.at(-1)
        expect(route?.meta.permission, i.to).toEqual(i.permission)
      }
      expect(router.resolve('/setup/properties/3').matched.at(-1)?.meta.permission).toBe('property.manage')
    })
  })

  it('finds the item of a path by its longest route', () => {
    expect(activeNav('/')?.item.id).toBe('dashboard')
    expect(activeNav('/reservations')?.item.id).toBe('reservations')
    expect(activeNav('/reservations/tape')?.item.id).toBe('tapeChart')
    expect(activeNav('/reservations/12')?.item.id).toBe('reservations')
    expect(activeNav('/setup/properties/new')?.item.id).toBe('properties')
    expect(activeNav('/housekeeping/tasks')?.item.id).toBe('cleaningList')
    expect(activeNav('/housekeeping/my')?.item.id).toBe('myTasks')
    expect(findNavItem('myTasks')?.section.id).toBe('rooms')
    expect(activeNav('/housekeeping')?.item.id).toBe('housekeeping')
    expect(activeNav('/accounting/ledger')?.section.id).toBe('accounting')
    expect(activeNav('/payables/bills')?.section.id).toBe('payables')
    expect(activeNav('/tax/returns')?.section.id).toBe('tax')
    expect(activeNav('/bank/cards')?.section.id).toBe('bank')
    expect(activeNav('/budget/vs-actual')?.section.id).toBe('budget')
    expect(activeNav('/stays/3')).toBeNull()
    expect(activeNav('/reservationsX')).toBeNull()
  })

  it('splits finance into accounting, payables, tax, bank and budget, none of them a section called finance', () => {
    const ids = navigation.map((s) => s.id)
    for (const id of ['accounting', 'payables', 'tax', 'bank', 'budget']) expect(ids).toContain(id)
    expect(ids).not.toContain('finance')
    expect(navigation.find((s) => s.id === 'accounting')?.items.some((i) => i.id === 'journals')).toBe(true)
    expect(navigation.find((s) => s.id === 'tax')?.items.some((i) => i.id === 'taxReturns')).toBe(true)
  })

  it('has the Performance page in the end of the day, shown only to who may view reports', () => {
    const named = items.filter((i) => ['myTasks', 'performance'].includes(i.id))
    expect(named.map((i) => [i.id, i.to, i.permission])).toEqual([['myTasks', '/housekeeping/my', 'housekeeping.update'], ['performance', '/performance', 'report.view']])
    expect(findNavItem('performance')?.section.id).toBe('endOfDay')
    const ids = (can?: (p: string) => boolean) => visibleNavigation(false, can).flatMap((s) => s.items.map((i) => i.id))
    expect(ids((p) => p === 'report.view')).toContain('performance')
    expect(ids(() => false)).not.toContain('performance')
    expect(ids()).not.toContain('performance')
    expect(ids(() => false)).toContain('dashboard') // the page of the day is for everyone
    expect(ids((p) => p === 'housekeeping.update')).toContain('myTasks') // the page of the corridor is for who cleans
    expect(ids(() => false)).not.toContain('myTasks')
  })

  it('keeps an item that names a permission from those who do not have it', () => {
    const menu = [{ ...navigation[0]!, items: [{ id: 'performance', to: '/performance', permission: 'report.view' }, { id: 'dashboard', to: '/' }] }]
    const ids = (can?: (p: string) => boolean, admin = false) => visibleNavigation(admin, can, menu).flatMap((s) => s.items.map((i) => i.id))
    expect(ids((p) => p === 'report.view')).toEqual(['performance', 'dashboard'])
    expect(ids(() => false)).toEqual(['dashboard'])
    expect(ids()).toEqual(['dashboard']) // nobody to ask: not shown
    expect(ids(() => false, true)).toEqual(['dashboard']) // an administrator does not skip the permission
    const adminOnly = [{ ...navigation[0]!, items: [{ id: 'x', to: '/x', adminOnly: true, permission: 'report.view' }] }]
    expect(visibleNavigation(false, () => true, adminOnly)).toEqual([]) // both rules hold
    expect(visibleNavigation(true, () => true, adminOnly)).toHaveLength(1)
  })

  describe('for a housekeeper (housekeeping.update, maintenance.report, lostfound.report)', () => {
    const held = new Set(['housekeeping.update', 'maintenance.report', 'lostfound.report'])
    const seen = visibleNavigation(false, (p) => held.has(p)).flatMap((s) => s.items.map((i) => i.id))
    const sections = visibleNavigation(false, (p) => held.has(p)).map((s) => s.id)

    it('does not show the pages of the front desk, the books, tax and the bank', () => {
      for (const id of ['reservations', 'tapeChart', 'availability', 'arrivals', 'inHouse', 'departures', 'walkIn', 'guests', 'groups', 'folios', 'cashier', 'cityLedger', 'nightAudit', 'reports', 'performance', 'chartOfAccounts', 'controlAccounts', 'suppliers', 'taxStatus', 'taxOwed', 'bankAccounts', 'budgets', 'auditTrail']) {
        expect(seen, id).not.toContain(id)
      }
      for (const id of ['accounting', 'payables', 'tax', 'bank', 'budget', 'billing']) expect(sections, id).not.toContain(id)
    })
    it('keeps the pages the housekeeper works in', () => {
      for (const id of ['dashboard', 'roomStatus', 'housekeeping', 'myTasks', 'cleaningList', 'maintenance', 'lostFound']) expect(seen, id).toContain(id)
    })
  })

  it('shows an item that names several permissions to who has any of them', () => {
    expect(permitted({ permission: ['a', 'b'] }, (p) => p === 'b')).toBe(true)
    expect(permitted({ permission: ['a', 'b'] }, () => false)).toBe(false)
    expect(permitted({}, () => false)).toBe(true)
  })

  it('names the permission of the read endpoint on every page, except the ones that every member of the property may read', () => {
    const open = ['dashboard', 'roomStatus', 'housekeeping', 'myTasks', 'cleaningList', 'roomBlocks', 'properties', 'users', 'roles']
    const bare = items.filter((i) => !i.permission && !i.adminOnly).map((i) => i.id)
    expect(bare.sort()).toEqual(open.filter((id) => !['properties', 'users', 'roles', 'myTasks'].includes(id)).sort())
  })

  it('finds an item by its id with its section, and nothing for an id that is gone', () => {
    expect(findNavItem('tapeChart')?.section.id).toBe('frontDesk')
    expect(findNavItem('journals')?.section.id).toBe('accounting')
    expect(findNavItem('nope')).toBeUndefined()
  })
})

describe('the modules of the plan', () => {
  const all = () => true
  const ids = (sections: ReturnType<typeof visibleNavigation>) => sections.flatMap((s) => s.items.map((i) => i.id))

  it('leaves out the pages of a module that the plan does not have, and shows them when it does', () => {
    const without = ids(visibleNavigation(true, all, undefined, (m) => m !== 'POS'))
    const withPos = ids(visibleNavigation(true, all, undefined, () => true))
    for (const id of ['posOutlets', 'posProducts', 'posMenuSetup', 'posFloorPlan', 'posStations', 'outletCharges']) {
      expect(without, id).not.toContain(id)
      expect(withPos, id).toContain(id)
    }
    expect(without).toContain('folios') // the core is never switched off
  })

  it('shows every page when the plan is not known yet', () => {
    expect(ids(visibleNavigation(true, all))).toContain('posOutlets')
  })

  it('asks for a permission of its own for each page of POS, and the same one in the router', () => {
    for (const i of navigation.flatMap((s) => s.items).filter((x) => x.module === 'POS')) expect(i.permission, i.id).toBeTruthy()
  })
})
