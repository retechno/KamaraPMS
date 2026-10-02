import { DOMWrapper, flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it } from 'vitest'
import { defineComponent, ref } from 'vue'
import Combobox from './Combobox.vue'

const options = [
  { value: 0, label: 'Choose an account' },
  { value: 1, label: '1110 · Cash on hand' },
  { value: 2, label: '4110 · Room revenue' },
  { value: 3, label: '4120 · Minibar revenue' },
]

function mountBox(initial: number | null = 0) {
  const Host = defineComponent({
    components: { Combobox },
    setup: () => ({ picked: ref<number | null>(initial), options }),
    template: '<Combobox v-model="picked" :options="options" name="account" id="acc" /><output data-testid="picked">{{ picked }}</output>',
  })
  return mount(Host, { attachTo: document.body })
}
const $ = (sel: string) => new DOMWrapper(document.body.querySelector(sel) as Element)
const all = (sel: string) => Array.from(document.body.querySelectorAll(sel))

describe('Combobox', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
  })

  it('shows the chosen label and keeps a native field with the name and every option', async () => {
    mountBox(2)
    await flushPromises()
    expect((document.getElementById('acc') as HTMLInputElement).value).toBe('4110 · Room revenue')
    expect(all('select[name=account] option').map((o) => o.textContent)).toEqual(options.map((o) => o.label))
    expect((document.querySelector('select[name=account]') as HTMLSelectElement).value).toBe('2')
  })

  it('narrows the list by what is typed and picks with a click', async () => {
    mountBox(0)
    await flushPromises()
    const input = $('#acc')
    await input.trigger('focus')
    await input.setValue('revenue')
    await flushPromises()
    expect(all('[role=option]').map((o) => o.textContent?.trim())).toEqual(['4110 · Room revenue', '4120 · Minibar revenue'])
    ;(all('[role=option]')[1] as HTMLElement).click()
    await flushPromises()
    expect($('[data-testid=picked]').text()).toBe('3')
  })

  it('says when nothing matches', async () => {
    mountBox(0)
    await flushPromises()
    await $('#acc').trigger('focus')
    await $('#acc').setValue('zzz')
    await flushPromises()
    expect(all('[role=option]')).toHaveLength(0)
    expect(document.body.textContent).toContain('Nothing to show.')
  })

  it('follows the native field, so a form reads it by name', async () => {
    mountBox(0)
    await flushPromises()
    await $('select[name=account]').setValue('1')
    expect($('[data-testid=picked]').text()).toBe('1')
    expect((document.getElementById('acc') as HTMLInputElement).value).toBe('1110 · Cash on hand')
  })
})
