import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, describe, expect, it } from 'vitest'
import { defineComponent, h, ref, withDirectives } from 'vue'
import { vAutofocus } from './autofocus'

let wrapper: VueWrapper | undefined
afterEach(() => { wrapper?.unmount(); wrapper = undefined })

interface Opts { off?: boolean; marked?: boolean; disabledFirst?: boolean }

function mountForm(opts: Opts = {}) {
  const Form = defineComponent({
    setup: () => () => withDirectives(
      h('form', { 'data-testid': 'f' }, [
        h('input', { name: 'a', disabled: opts.disabledFirst }),
        h('input', { name: 'b', 'data-autofocus': opts.marked ? '' : undefined, value: 'keep' }),
        h('button', { type: 'submit' }, 'go'),
      ]),
      [[vAutofocus, opts.off ? false : undefined]],
    ),
  })
  wrapper = mount(Form, { attachTo: document.body })
  return wrapper
}

const focused = () => (document.activeElement as HTMLInputElement | null)?.name

describe('v-autofocus', () => {
  it('focuses the first field of a form that appears', async () => {
    mountForm()
    await flushPromises()
    expect(focused()).toBe('a')
  })

  it('prefers the field marked data-autofocus', async () => {
    mountForm({ marked: true })
    await flushPromises()
    expect(focused()).toBe('b')
  })

  it('skips a disabled field', async () => {
    mountForm({ disabledFirst: true })
    await flushPromises()
    expect(focused()).toBe('b')
  })

  it('does nothing when switched off, and never touches a value or submits', async () => {
    const w = mountForm({ off: true })
    await flushPromises()
    expect(document.activeElement?.tagName).not.toBe('INPUT')
    expect((w.get('input[name=b]').element as HTMLInputElement).value).toBe('keep')
  })

  it('does not take the focus from a field the person already uses', async () => {
    const outer = document.createElement('input')
    outer.name = 'outer'
    document.body.appendChild(outer)
    outer.focus()
    mountForm()
    await flushPromises()
    // the form is not the person's place yet: a field outside of it had the focus, so the form claims it as it appears
    expect(focused()).toBe('a')
    outer.remove()
  })
  it('focuses again each time its tick changes, and not when something else re-renders', async () => {
    const tick = ref(0)
    const Form = defineComponent({
      setup: () => () => withDirectives(
        h('form', [h('input', { name: 'a' }), h('input', { name: 'b' }), h('span', tick.value > 99 ? 'x' : '')]),
        [[vAutofocus, tick.value]],
      ),
    })
    wrapper = mount(Form, { attachTo: document.body })
    await flushPromises()
    expect(focused()).toBe('a')
    ;(wrapper.get('input[name=b]').element as HTMLInputElement).focus()
    await wrapper.vm.$forceUpdate()
    await flushPromises()
    expect(focused()).toBe('b') // a re-render with the same tick leaves the person where they are
    ;(document.activeElement as HTMLElement).blur()
    tick.value++ // the opening button was pressed again
    await flushPromises()
    expect(focused()).toBe('a')
  })
  it('focuses again when the subject it is open for changes (edit another row)', async () => {
    const subject = ref<object>({ id: 1 })
    const Form = defineComponent({
      setup: () => () => withDirectives(h('form', [h('input', { name: 'a' }), h('input', { name: 'b' })]), [[vAutofocus, subject.value]]),
    })
    wrapper = mount(Form, { attachTo: document.body })
    await flushPromises()
    ;(wrapper.get('input[name=b]').element as HTMLInputElement).focus()
    ;(document.activeElement as HTMLElement).blur() // the person pressed Edit on another row: the focus is on that button
    subject.value = { id: 2 }
    await flushPromises()
    expect(focused()).toBe('a')
  })
})
