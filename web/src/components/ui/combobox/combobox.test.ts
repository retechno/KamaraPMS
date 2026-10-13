import { DOMWrapper, flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it } from 'vitest'
import { defineComponent, ref } from 'vue'
import { focusProgrammatically } from '@/lib/focus'
import Combobox from './Combobox.vue'

const options = [
  { value: 0, label: 'Choose an account' },
  { value: 1, label: '1110 · Cash on hand' },
  { value: 2, label: '4110 · Room revenue' },
  { value: 3, label: '4120 · Minibar revenue' },
]

const mounted: { unmount: () => void }[] = []

function mountBox(initial: number | null = 0) {
  const Host = defineComponent({
    components: { Combobox },
    setup: () => ({ picked: ref<number | null>(initial), options }),
    template: '<Combobox v-model="picked" :options="options" name="account" id="acc" /><output data-testid="picked">{{ picked }}</output>',
  })
  const wrapper = mount(Host, { attachTo: document.body })
  mounted.push(wrapper)
  return wrapper
}
const $ = (sel: string) => new DOMWrapper(document.body.querySelector(sel) as Element)
const all = (sel: string) => Array.from(document.body.querySelectorAll(sel))

describe('Combobox', () => {
  beforeEach(() => {
    // A list left open by an earlier test would answer the focus of the next one after its page was removed.
    for (const w of mounted.splice(0)) w.unmount()
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
  it('keeps its list closed when the page puts the focus on it', async () => {
    const w = mountBox(0)
    focusProgrammatically(document.body.querySelector('input[id=acc]') as HTMLInputElement)
    await flushPromises()
    expect(all('[data-slot=combobox-content]')).toHaveLength(0)
    expect(document.activeElement).toBe(document.body.querySelector('input[id=acc]'))
    w.unmount()
  })

  it('opens its list when the person focuses it', async () => {
    mountBox(0)
    ;(document.body.querySelector('input[id=acc]') as HTMLInputElement).focus()
    await flushPromises()
    expect(all('[data-slot=combobox-content]').length).toBeGreaterThan(0)
  })

  it('does not open on focus when asked not to', async () => {
    const Host = defineComponent({
      components: { Combobox },
      setup: () => ({ picked: ref<number | null>(0), options }),
      template: '<Combobox v-model="picked" :options="options" :open-on-focus="false" name="account" id="acc" />',
    })
    mounted.push(mount(Host, { attachTo: document.body }))
    ;(document.body.querySelector('input[id=acc]') as HTMLInputElement).focus()
    await flushPromises()
    expect(all('[data-slot=combobox-content]')).toHaveLength(0)
  })

  describe('a list opened over a chosen value', () => {
    const labels = () => all('[role=option]').map((o) => o.textContent?.trim())
    const input = () => document.getElementById('acc') as HTMLInputElement
    const every = options.map((o) => o.label)
    const open = async () => {
      input().focus()
      await flushPromises()
    }

    it('shows every option, the chosen one checked and highlighted', async () => {
      mountBox(2)
      await flushPromises()
      await open()
      expect(labels()).toEqual(every)
      const chosen = all('[role=option]')[2] as Element
      expect(chosen.getAttribute('aria-selected')).toBe('true')
      expect(chosen.hasAttribute('data-highlighted')).toBe(true)
    })

    it('selects the text of the field when it opens, so typing replaces it', async () => {
      mountBox(2)
      await flushPromises()
      await open()
      expect(input().selectionStart).toBe(0)
      expect(input().selectionEnd).toBe(input().value.length)
    })

    it('narrows by what is typed, and shows every option again the next time it opens', async () => {
      mountBox(2)
      await flushPromises()
      await open()
      await $('#acc').setValue('4110')
      await flushPromises()
      expect(labels()).toEqual(['4110 · Room revenue'])
      await $('#acc').trigger('keydown', { key: 'Escape' })
      await flushPromises()
      expect(all('[role=option]')).toHaveLength(0)
      await $('#acc').trigger('click') // the field keeps the focus, so a click opens it again
      await flushPromises()
      expect(labels()).toEqual(every)
    })

    it('keeps the value and the label when it is closed without a choice', async () => {
      mountBox(2)
      await flushPromises()
      await open()
      await $('#acc').setValue('zz')
      await $('#acc').trigger('keydown', { key: 'Escape' })
      await flushPromises()
      expect($('[data-testid=picked]').text()).toBe('2')
      expect(input().value).toBe('4110 · Room revenue')
    })

    it('does not empty the value when the text is cleared and the list is closed', async () => {
      mountBox(2)
      await flushPromises()
      await open()
      await $('#acc').setValue('')
      await $('#acc').trigger('keydown', { key: 'Escape' })
      await flushPromises()
      expect($('[data-testid=picked]').text()).toBe('2')
      expect(input().value).toBe('4110 · Room revenue')
    })

    it('still chooses with the arrows and Enter', async () => {
      mountBox(1)
      await flushPromises()
      await open()
      await $('#acc').trigger('keydown', { key: 'ArrowDown' })
      await $('#acc').trigger('keydown', { key: 'Enter' })
      await flushPromises()
      expect($('[data-testid=picked]').text()).toBe('2')
      expect(input().value).toBe('4110 · Room revenue')
    })
  })
})
