import { describe, expect, it } from 'vitest'
import { i18n } from '@/i18n'
import { router } from '@/router'
import { activeNav, navigation, visibleNavigation } from './navigation'

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

  it('has a label in every language for every section, group and item', () => {
    const messages = i18n.global.messages.value as unknown as Record<string, { nav: { sections: object; groups: object; items: object } }>
    for (const locale of ['en', 'id']) {
      const nav = messages[locale]!.nav
      for (const s of navigation) expect(Object.keys(nav.sections), `${locale} section ${s.id}`).toContain(s.id)
      for (const i of items) {
        expect(Object.keys(nav.items), `${locale} item ${i.id}`).toContain(i.id)
        if (i.group) expect(Object.keys(nav.groups), `${locale} group ${i.group}`).toContain(i.group)
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
    expect(activeNav('/accounting/ledger')?.section.id).toBe('finance')
    expect(activeNav('/stays/3')).toBeNull()
    expect(activeNav('/reservationsX')).toBeNull()
  })
})
