import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import RowMenu from './RowMenu.vue'
import type { RowAction } from './rowActions'
import TableRowActions from './TableRowActions.vue'

let wrapper: VueWrapper | null = null
afterEach(() => {
  wrapper?.unmount()
  wrapper = null
  document.body.innerHTML = ''
})

const router = () => createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })

describe('TableRowActions', () => {
  it('shows the main actions as buttons (at most `maxPrimary`) and every other in the "..." menu', async () => {
    const run = vi.fn()
    const actions: RowAction[] = [
      { key: 'a', label: 'A', primary: true, testId: 'btn-a', onSelect: run },
      { key: 'b', label: 'B', primary: true, testId: 'btn-b' },
      { key: 'c', label: 'C', primary: true, testId: 'btn-c' },
      { key: 'd', label: 'D' },
    ]
    wrapper = mount(TableRowActions, { props: { actions, maxPrimary: 2 }, attachTo: document.body, global: { plugins: [router()] } })
    expect(wrapper.findAll('[data-testid^=btn-]').map((b) => b.attributes('data-testid'))).toEqual(['btn-a', 'btn-b'])
    await wrapper.get('[data-testid=btn-a]').trigger('click')
    expect(run).toHaveBeenCalledTimes(1)
    await wrapper.get('[data-slot=row-menu-trigger]').trigger('click')
    await flushPromises()
    expect(Array.from(document.body.querySelectorAll('[data-slot=row-menu] [data-testid]')).map((e) => e.getAttribute('data-testid'))).toEqual(['btn-c', 'menu-d']) // an action keeps its own test id wherever it is shown
  })

  it('has no menu when every action is a main one, and a link where an action has a page', async () => {
    wrapper = mount(TableRowActions, { props: { actions: [{ key: 'open', label: 'Open', to: '/x', primary: true, testId: 'open' }] }, global: { plugins: [router()] } })
    expect(wrapper.get('[data-testid=open]').attributes('href')).toBe('/x')
    expect(wrapper.find('[data-slot=row-menu-trigger]').exists()).toBe(false)
  })

  it('runs the action that was chosen in the menu, passes by a disabled one, and marks a destructive one', async () => {
    const drop = vi.fn()
    wrapper = mount(TableRowActions, {
      props: { actions: [{ key: 'drop', label: 'Drop', destructive: true, onSelect: drop }, { key: 'off', label: 'Off', disabled: true }] },
      attachTo: document.body,
      global: { plugins: [router()] },
    })
    await wrapper.get('[data-slot=row-menu-trigger]').trigger('click')
    await flushPromises()
    expect((document.body.querySelector('[data-testid=menu-off]') as HTMLButtonElement).disabled).toBe(true)
    expect(document.body.querySelector('[data-testid=menu-drop]')?.className).toContain('text-destructive')
    ;(document.body.querySelector('[data-testid=menu-drop]') as HTMLElement).click()
    await flushPromises()
    expect(drop).toHaveBeenCalledTimes(1)
  })
})

describe('RowMenu: a link item', () => {
  it('goes to its page, closes the menu, and takes another test id', async () => {
    wrapper = mount(RowMenu, { props: { items: [{ key: 'edit', label: 'Edit', to: '/rows/1/edit', testId: 'my-edit' }, { key: 'x', label: 'X' }] }, attachTo: document.body, global: { plugins: [router()] } })
    await wrapper.get('[data-slot=row-menu-trigger]').trigger('click')
    await flushPromises()
    const link = document.body.querySelector('[data-testid=my-edit]') as HTMLAnchorElement
    expect(link.getAttribute('href')).toBe('/rows/1/edit')
    expect(document.body.querySelector('[data-testid=menu-x]')?.tagName).toBe('BUTTON')
    link.click()
    await flushPromises()
    expect(document.body.querySelector('[data-slot=row-menu]')).toBeNull()
  })
})
