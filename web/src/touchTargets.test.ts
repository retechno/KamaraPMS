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
    expect(size('default')).toEqual(expect.arrayContaining(['h-9', 'pointer-coarse:min-h-11']))
    expect(size('sm')).toEqual(expect.arrayContaining(['h-8', 'pointer-coarse:min-h-11']))
    expect(size('lg')).toEqual(expect.arrayContaining(['h-10', 'pointer-coarse:min-h-11']))
    expect(size('icon')).toEqual(expect.arrayContaining(['size-9', 'pointer-coarse:size-11']))
  })

  it('does the same for a text input, a select and the field of a date', () => {
    expect(mount(Input).classes()).toContain('pointer-coarse:min-h-11')
    expect(mount(NativeSelect).classes()).toContain('pointer-coarse:min-h-11')
    expect(mount(DateInput, { props: { modelValue: '' } }).get('input').classes()).toContain('pointer-coarse:min-h-11')
  })

  it('makes the "..." trigger of a row at least 44 px wide', async () => {
    const w = mount(RowMenu, { props: { items: [{ key: 'a', label: 'A' }] }, attachTo: document.body })
    await flushPromises()
    const trigger = document.body.querySelector('[data-slot=row-menu-trigger]')!
    expect(trigger.className).toContain('pointer-coarse:min-w-11')
    expect(trigger.className).toContain('pointer-coarse:min-h-11') // from the button size
    w.unmount()
    document.body.innerHTML = ''
  })

  it('widens the hit area of a room link with an ::after box, so the row does not grow', () => {
    const pinia = createPinia()
    setActivePinia(pinia)
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: { template: '<div />' } }] })
    const w = mount(RoomLink, { props: { roomId: 1, number: '201' }, global: { plugins: [pinia, router] } })
    expect(w.classes()).toEqual(expect.arrayContaining(['relative', 'pointer-coarse:after:absolute', 'pointer-coarse:after:-inset-x-3']))
  })

  it('has a rule for the checkboxes and the labels around them in the style sheet', () => {
    const css = readFileSync('src/assets/tailwind.css', 'utf8')
    const rule = css.slice(css.indexOf('@media (pointer: coarse)'))
    expect(rule).toMatch(/input\[type='checkbox'\][\s\S]*width: 1\.5rem/)
    expect(rule).toMatch(/label:has\(> input\[type='checkbox'\]\)[\s\S]*min-height: 2\.75rem/)
  })
})
