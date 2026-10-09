import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, describe, expect, it } from 'vitest'
import { defineComponent, h } from 'vue'
import FormField from './FormField.vue'

let wrapper: VueWrapper | undefined
afterEach(() => { wrapper?.unmount(); wrapper = undefined })

const Form = defineComponent({
  props: { first: { type: String, default: undefined }, second: { type: String, default: undefined } },
  setup: (p) => () => h('form', [
    h(FormField, { label: 'A', error: p.first }, { default: ({ id, invalid }: { id: string; invalid: boolean }) => h('input', { id, name: 'a', 'aria-invalid': invalid }) }),
    h(FormField, { label: 'B', error: p.second }, { default: ({ id, invalid }: { id: string; invalid: boolean }) => h('input', { id, name: 'b', 'aria-invalid': invalid }) }),
  ]),
})

const focused = () => (document.activeElement as HTMLInputElement | null)?.name

describe('FormField', () => {
  it('moves the focus to the first refused field when a refusal arrives', async () => {
    wrapper = mount(Form, { attachTo: document.body })
    await wrapper.setProps({ first: 'Required', second: 'Invalid' })
    await flushPromises()
    expect(focused()).toBe('a')
  })

  it('leaves the focus alone when the form is drawn with a refusal already on it', async () => {
    wrapper = mount(Form, { props: { second: 'Invalid' }, attachTo: document.body })
    await flushPromises()
    expect(focused()).toBeUndefined()
  })

  it('does not steal the focus from a refused field the person is correcting', async () => {
    wrapper = mount(Form, { props: { first: 'Required' }, attachTo: document.body })
    ;(wrapper.get('input[name=b]').element as HTMLInputElement).focus()
    await wrapper.setProps({ second: 'Invalid' })
    await flushPromises()
    expect(focused()).toBe('b')
  })
})
