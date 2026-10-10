import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, describe, expect, it } from 'vitest'
import { defineComponent, h, ref } from 'vue'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuRadioGroup, DropdownMenuRadioItem, DropdownMenuSeparator, DropdownMenuTrigger } from '.'

let wrapper: VueWrapper | null = null
afterEach(() => {
  wrapper?.unmount()
  wrapper = null
  document.body.innerHTML = ''
})

const choice = ref('id')
const Menu = defineComponent({
  setup(_, { emit }) {
    return () =>
      h(DropdownMenu, null, () => [
        h(DropdownMenuTrigger, { asChild: true }, () => h('button', { type: 'button', 'data-testid': 'trigger' }, 'Menu')),
        h(DropdownMenuContent, null, () => [
          h(DropdownMenuLabel, null, () => 'Language'),
          h(DropdownMenuRadioGroup, { modelValue: choice.value, 'onUpdate:modelValue': (v: unknown) => (choice.value = String(v)) }, () => [
            h(DropdownMenuRadioItem, { value: 'id', 'data-testid': 'lang-id' }, () => 'Indonesia'),
            h(DropdownMenuRadioItem, { value: 'en', 'data-testid': 'lang-en' }, () => 'English'),
          ]),
          h(DropdownMenuSeparator),
          h(DropdownMenuItem, { 'data-testid': 'out', onSelect: () => emit('out') }, () => 'Sign out'),
        ]),
      ])
  },
  emits: ['out'],
})

/** reka-ui opens a menu on a primary-button press and on Enter or Space; jsdom cannot make a pointer event with a button, so the tests press Enter. */
async function open(w: VueWrapper): Promise<void> {
  await w.get('[data-testid=trigger]').trigger('keydown', { key: 'Enter' })
  await flushPromises()
}

describe('DropdownMenu', () => {
  it('opens on Enter on its button and shows its items in a portal, not before', async () => {
    wrapper = mount(Menu, { attachTo: document.body })
    expect(document.body.querySelector('[data-testid=out]')).toBeNull()
    await open(wrapper)
    expect(document.body.querySelector('[role=menu]')).not.toBeNull()
    expect(document.body.querySelector('[data-testid=out]')?.textContent).toBe('Sign out')
  })

  it('marks the chosen radio item, changes the choice and closes', async () => {
    choice.value = 'id'
    wrapper = mount(Menu, { attachTo: document.body })
    await open(wrapper)
    expect(document.body.querySelector('[data-testid=lang-id]')?.getAttribute('aria-checked')).toBe('true')
    expect(document.body.querySelector('[data-testid=lang-en]')?.getAttribute('aria-checked')).toBe('false')
    ;(document.body.querySelector('[data-testid=lang-en]') as HTMLElement).click()
    await flushPromises()
    expect(choice.value).toBe('en')
    expect(document.body.querySelector('[role=menu]')).toBeNull()
  })

  it('runs an item and closes', async () => {
    wrapper = mount(Menu, { attachTo: document.body })
    await open(wrapper)
    ;(document.body.querySelector('[data-testid=out]') as HTMLElement).click()
    await flushPromises()
    expect(wrapper.emitted('out')).toHaveLength(1)
    expect(document.body.querySelector('[role=menu]')).toBeNull()
  })
})
