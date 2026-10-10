import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, describe, expect, it } from 'vitest'
import RowMenu from './RowMenu.vue'

let wrapper: VueWrapper | null = null
afterEach(() => {
  wrapper?.unmount()
  wrapper = null
  document.body.innerHTML = ''
})

const items = [{ key: 'edit', label: 'Edit' }, { key: 'delete', label: 'Delete', destructive: true }, { key: 'off', label: 'Switch off', disabled: true }]

describe('RowMenu', () => {
  it('folds the actions of a row into one "More actions" button, and draws them only when it is open', async () => {
    wrapper = mount(RowMenu, { attachTo: document.body, props: { items } })
    const trigger = wrapper.get('[data-slot=row-menu-trigger]')
    expect(trigger.attributes('aria-label')).toBe('More actions')
    expect(document.body.querySelector('[data-testid=menu-edit]')).toBeNull()
    await trigger.trigger('click')
    await flushPromises()
    expect(document.body.querySelector('[data-testid=menu-edit]')?.textContent).toBe('Edit')
    expect(document.body.querySelector('[data-testid=menu-delete]')?.className).toContain('text-destructive')
    expect((document.body.querySelector('[data-testid=menu-off]') as HTMLButtonElement).disabled).toBe(true)
  })

  it('runs the chosen action and closes', async () => {
    wrapper = mount(RowMenu, { attachTo: document.body, props: { items } })
    await wrapper.get('[data-slot=row-menu-trigger]').trigger('click')
    await flushPromises()
    ;(document.body.querySelector('[data-testid=menu-edit]') as HTMLElement).click()
    await flushPromises()
    expect(wrapper.emitted('select')).toEqual([['edit']])
    expect(document.body.querySelector('[data-testid=menu-edit]')).toBeNull()
  })

  it('draws nothing when the row has no action', () => {
    wrapper = mount(RowMenu, { props: { items: [] } })
    expect(wrapper.find('[data-slot=row-menu-trigger]').exists()).toBe(false)
  })
})
