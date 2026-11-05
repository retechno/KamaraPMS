import { flushPromises, mount } from '@vue/test-utils'
import { readFileSync } from 'node:fs'
import { createPinia, setActivePinia } from 'pinia'
import { describe, expect, it } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import RoomLink from '@/components/app/RoomLink.vue'
import RowMenu from '@/components/app/RowMenu.vue'
import { DateInput } from '@/components/ui/date-input'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'

/**
 * A finger needs a target of 44 px. jsdom does not evaluate media queries, so these tests read the classes: `pointer-coarse:` is what turns the compact size of a mouse
 * into 44 px on a phone or a tablet (it is measured in a real browser, see the pull request).
 */
describe('touch targets (pointer: coarse)', () => {
  it('gives every size of a button a minimum of 44 px and keeps the compact size of a mouse', () => {
    const size = (s: 'default' | 'sm' | 'lg' | 'icon') => mount(Button, { props: { size: s }, slots: { default: 'x' } }).classes()
    expect(size('default')).toEqual(expect.arrayContaining(['h-9', 'pointer-coarse:min-h-[44px]']))
    expect(size('sm')).toEqual(expect.arrayContaining(['h-8', 'pointer-coarse:min-h-[44px]']))
    expect(size('lg')).toEqual(expect.arrayContaining(['h-10', 'pointer-coarse:min-h-[44px]']))
    expect(size('icon')).toEqual(expect.arrayContaining(['size-9', 'pointer-coarse:size-[44px]']))
  })

  it('does the same for a text input, a select and the field of a date', () => {
    expect(mount(Input).classes()).toContain('pointer-coarse:min-h-[44px]')
    expect(mount(NativeSelect).classes()).toContain('pointer-coarse:min-h-[44px]')
    expect(mount(DateInput, { props: { modelValue: '' } }).get('input').classes()).toContain('pointer-coarse:min-h-[44px]')
  })

  it('makes the "..." trigger of a row at least 44 px wide', async () => {
    const w = mount(RowMenu, { props: { items: [{ key: 'a', label: 'A' }] }, attachTo: document.body })
    await flushPromises()
    const trigger = document.body.querySelector('[data-slot=row-menu-trigger]')!
    expect(trigger.className).toContain('pointer-coarse:min-w-[44px]')
    expect(trigger.className).toContain('pointer-coarse:min-h-[44px]') // from the button size
    w.unmount()
    document.body.innerHTML = ''
  })

  it('widens the hit area of a room link with an ::after box, so the row does not grow', () => {
    const pinia = createPinia()
    setActivePinia(pinia)
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: { template: '<div />' } }] })
    const w = mount(RoomLink, { props: { roomId: 1, number: '201' }, global: { plugins: [pinia, router] } })
    expect(w.classes()).toEqual(expect.arrayContaining(['pointer-coarse:relative', 'pointer-coarse:after:absolute', 'pointer-coarse:after:-inset-x-[12px]', 'pointer-coarse:after:-inset-y-[14px]']))
  })

  it('has a rule for the checkboxes and the labels around them in the style sheet', () => {
    const css = readFileSync('src/assets/tailwind.css', 'utf8')
    const rule = css.slice(css.indexOf('@media (pointer: coarse)'))
    expect(rule).toMatch(/input\[type='checkbox'\][\s\S]*width: 24px/)
    expect(rule).toMatch(/label:has\(> input\[type='checkbox'\]\)[\s\S]*min-height: 44px/)
  })

  it('makes a plain link or button in a table cell or a card 44 px high, in the components layer so a utility still wins', () => {
    const css = readFileSync('src/assets/tailwind.css', 'utf8')
    expect(css).toMatch(/@layer components \{\s*:is\(td, \[data-slot='data-card'\]\) :is\(a, button\):not\(\[data-slot\]\) \{[^}]*min-height: 44px/)
  })

  it('gives the rows, the small buttons and the property select of the menu a 44 px height on a coarse pointer', () => {
    const nav = readFileSync('src/components/shell/SidebarNav.vue', 'utf8')
    expect(nav.match(/pointer-coarse:min-h-\[44px\]/g)?.length).toBeGreaterThanOrEqual(2) // the link of an item and the title of a section
    expect(nav.match(/pointer-coarse:size-\[44px\]/g)?.length).toBe(2) // "unpin" of a pinned item and "hide the last opened"
    expect(readFileSync('src/components/shell/PropertySwitcher.vue', 'utf8')).toContain('pointer-coarse:h-[44px]')
    expect(readFileSync('src/components/ui/dropdown-menu/DropdownMenuItem.vue', 'utf8')).toContain('pointer-coarse:min-h-[44px]')
  })

  it('keeps 8 px between the buttons of a row (the next step and the "...")', () => {
    expect(readFileSync('src/components/app/TableRowActions.vue', 'utf8')).toContain('pointer-coarse:gap-2')
  })
})
