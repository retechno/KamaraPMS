import { describe, expect, it } from 'vitest'
import { i18n } from '@/i18n'
import { router } from '@/router'
import { activeNav, findNavItem, navigation, visibleNavigation } from './navigation'

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
    expect(asUser).toContain('roomTypes')
  })

  it('finds the item of a path by its longest route', () => {
    expect(activeNav('/')?.item.id).toBe('dashboard')
    expect(activeNav('/reservations')?.item.id).toBe('reservations')
    expect(activeNav('/reservations/tape')?.item.id).toBe('tapeChart')
    expect(activeNav('/reservations/12')?.item.id).toBe('reservations')
    expect(activeNav('/setup/properties/new')?.item.id).toBe('properties')
    expect(activeNav('/housekeeping/tasks')?.item.id).toBe('cleaningList')
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

  it('keeps an item that names a permission from those who do not have it (no item of the menu uses this yet)', () => {
    expect(items.some((i) => i.permission)).toBe(false)
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

  it('finds an item by its id with its section, and nothing for an id that is gone', () => {
    expect(findNavItem('tapeChart')?.section.id).toBe('frontDesk')
    expect(findNavItem('journals')?.section.id).toBe('accounting')
    expect(findNavItem('nope')).toBeUndefined()
  })
})
