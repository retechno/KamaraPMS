import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, describe, expect, it } from 'vitest'
import { defineComponent, h, ref } from 'vue'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '.'

let wrapper: VueWrapper | undefined
afterEach(() => { wrapper?.unmount(); wrapper = undefined; document.body.innerHTML = '' })

const open = ref(true)
const Host = defineComponent({
  props: { marked: Boolean },
  setup: (p) => () => h('div', [
    h('div', { 'data-slot': 'toast-host' }, [h('button', { 'data-testid': 'toast-btn' }, 'toast')]),
    h(Dialog, { open: open.value, 'onUpdate:open': (v: boolean) => { open.value = v } }, () => h(DialogContent, null, () => [
      h(DialogTitle, null, () => 'T'),
      h(DialogDescription, null, () => 'D'),
      h('input', { name: 'first' }),
      h('input', { name: 'wanted', 'data-autofocus': p.marked ? '' : undefined }),
    ])),
  ]),
})

describe('DialogContent', () => {
  it('opens on the field marked data-autofocus', async () => {
    open.value = true
    wrapper = mount(Host, { props: { marked: true }, attachTo: document.body })
    await flushPromises()
    expect((document.activeElement as HTMLInputElement).name).toBe('wanted')
  })

  it('reopens on the marked field every time', async () => {
    open.value = true
    wrapper = mount(Host, { props: { marked: true }, attachTo: document.body })
    await flushPromises()
    open.value = false
    await flushPromises()
    open.value = true
    await flushPromises()
    expect((document.activeElement as HTMLInputElement).name).toBe('wanted')
  })

  it('stays open when a toast above it is clicked', async () => {
    open.value = true
    wrapper = mount(Host, { attachTo: document.body })
    await flushPromises()
    const btn = document.body.querySelector('[data-testid=toast-btn]') as HTMLElement
    btn.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true }))
    btn.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await flushPromises()
    expect(open.value).toBe(true)
  })
})
