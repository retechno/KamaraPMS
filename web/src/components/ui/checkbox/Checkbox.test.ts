import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, ref } from 'vue'
import { Checkbox } from '.'

const input = (w: ReturnType<typeof mount>) => w.get('input')

describe('Checkbox', () => {
  afterEach(() => vi.restoreAllMocks())

  it('is a label that holds the native box and the text, 44 px high for a finger and 24 px in the box', () => {
    const w = mount(Checkbox, { slots: { default: 'Send a copy' } })
    expect(w.element.tagName).toBe('LABEL')
    expect(w.classes()).toContain('inline-flex')
    expect(w.classes()).toContain('items-center')
    expect(w.classes()).toContain('pointer-coarse:min-h-[44px]')
    expect(w.element.contains(input(w).element)).toBe(true)
    expect(input(w).attributes('type')).toBe('checkbox')
    expect(input(w).classes()).toContain('accent-primary')
    expect(input(w).classes()).toContain('pointer-coarse:size-[24px]')
    expect(w.text()).toBe('Send a copy')
  })

  it('is the box alone in a 44 x 44 area when there is no text, and then it needs a name', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const named = mount(Checkbox, { attrs: { 'aria-label': 'Select row 1' } })
    expect(named.classes()).toContain('pointer-coarse:min-w-[44px]')
    expect(named.classes()).toContain('pointer-coarse:min-h-[44px]')
    expect(input(named).attributes('aria-label')).toBe('Select row 1')
    expect(warn).not.toHaveBeenCalled()
    mount(Checkbox) // no text and no name
    expect(warn).toHaveBeenCalledTimes(1)
    expect(String(warn.mock.calls[0]?.[0])).toContain('aria-label')
    // a box with text is not a box alone
    expect(mount(Checkbox, { slots: { default: 'x' } }).classes()).not.toContain('pointer-coarse:min-w-[44px]')
  })

  it('puts the attributes on the native input, and the label class on the label', () => {
    const w = mount(Checkbox, {
      attrs: { name: 'send_copy', 'data-testid': 'copy', id: 'copy-1', disabled: true, class: 'mt-1', 'aria-describedby': 'hint' },
      props: { labelClass: 'rounded-lg border' },
      slots: { default: 'Copy' },
    })
    const box = input(w)
    expect(box.attributes('name')).toBe('send_copy')
    expect(box.attributes('data-testid')).toBe('copy')
    expect(box.attributes('id')).toBe('copy-1')
    expect(box.attributes('disabled')).toBeDefined()
    expect(box.attributes('aria-describedby')).toBe('hint')
    expect(box.classes()).toContain('mt-1')
    expect(box.classes()).toContain('size-4') // its own classes stay
    expect(w.classes()).toContain('rounded-lg')
    expect(w.attributes('name')).toBeUndefined() // nothing of the caller on the label
    expect(w.attributes('data-testid')).toBeUndefined()
    expect(w.classes()).not.toContain('mt-1')
  })

  it('keeps the plain DOM for the tests that already read it: setValue and element.checked on the input', async () => {
    const Host = defineComponent({
      components: { Checkbox },
      setup: () => ({ on: ref(false) }),
      template: '<Checkbox v-model="on" name="agree">Agree</Checkbox>',
    })
    const w = mount(Host)
    expect((w.get('input[name=agree]').element as HTMLInputElement).checked).toBe(false)
    await w.get('input[name=agree]').setValue(true)
    expect((w.vm as unknown as { on: boolean }).on).toBe(true)
    expect((w.get('input[name=agree]').element as HTMLInputElement).checked).toBe(true)
    await w.get('input[name=agree]').setValue(false)
    expect((w.vm as unknown as { on: boolean }).on).toBe(false)
  })

  it('takes a boolean v-model: shows it and says what the person chose', async () => {
    const w = mount(Checkbox, { props: { modelValue: true }, slots: { default: 'x' } })
    expect((input(w).element as HTMLInputElement).checked).toBe(true)
    await w.setProps({ modelValue: false })
    expect((input(w).element as HTMLInputElement).checked).toBe(false)
    await input(w).setValue(true)
    expect(w.emitted('update:modelValue')?.at(-1)).toEqual([true])
  })

  it('takes an array v-model with a value, as a native checkbox does, and never changes the array it was given', async () => {
    const Host = defineComponent({
      components: { Checkbox },
      setup: () => ({ picked: ref<(number | string)[]>([2]) }),
      template: '<div><Checkbox v-model="picked" :value="1" aria-label="one" data-testid="one" /><Checkbox v-model="picked" :value="2" aria-label="two" data-testid="two" /><Checkbox v-model="picked" value="3" aria-label="three" data-testid="three" /></div>',
    })
    const w = mount(Host)
    const picked = () => (w.vm as unknown as { picked: unknown[] }).picked
    const box = (id: string) => w.get(`[data-testid=${id}]`)
    const checked = (id: string) => (box(id).element as HTMLInputElement).checked
    expect([checked('one'), checked('two'), checked('three')]).toEqual([false, true, false])
    const before = picked()
    await box('one').setValue(true)
    expect(picked()).toEqual([2, 1])
    expect(before).toEqual([2]) // a new array, not the old one changed
    await box('three').setValue(true)
    expect(picked()).toEqual([2, 1, '3'])
    await box('two').setValue(false)
    expect(picked()).toEqual([1, '3'])
    expect([checked('one'), checked('two'), checked('three')]).toEqual([true, false, true])
    await w.get('[data-testid=one]').setValue(false)
    expect(picked()).toEqual(['3'])
  })

  it('reads a value of a number and the same value as text as the same value, as a native v-model does', async () => {
    const w = mount(Checkbox, { props: { modelValue: ['7'], value: 7 }, attrs: { 'aria-label': 'seven' } })
    expect((input(w).element as HTMLInputElement).checked).toBe(true)
    await input(w).setValue(false)
    expect(w.emitted('update:modelValue')?.at(-1)).toEqual([[]])
  })

  it('has the box half on for "select all" that holds some of its rows: the DOM property and aria-checked', async () => {
    const w = mount(Checkbox, { props: { modelValue: false, indeterminate: true }, attrs: { 'aria-label': 'all' } })
    expect((input(w).element as HTMLInputElement).indeterminate).toBe(true)
    expect(input(w).attributes('aria-checked')).toBe('mixed')
    await w.setProps({ indeterminate: false })
    expect((input(w).element as HTMLInputElement).indeterminate).toBe(false)
    expect(input(w).attributes('aria-checked')).toBeUndefined()
    await w.setProps({ indeterminate: true })
    await input(w).setValue(true) // a click clears it in the browser; the page says whether it is still so
    await flushPromises()
    expect((input(w).element as HTMLInputElement).indeterminate).toBe(true)
  })

  it('is controlled by modelValue with @change, as :checked was: it shows what it is given, and the listener of the page runs on the input', async () => {
    const onChange = vi.fn()
    const w = mount(Checkbox, { props: { modelValue: false }, attrs: { 'aria-label': 'pick', onChange } })
    await input(w).setValue(true)
    await flushPromises()
    expect(onChange).toHaveBeenCalledTimes(1)
    expect((onChange.mock.calls[0]?.[0] as Event).target).toBe(input(w).element)
    expect((input(w).element as HTMLInputElement).checked).toBe(false) // the page did not take it: the box goes back
    await w.setProps({ modelValue: true })
    expect((input(w).element as HTMLInputElement).checked).toBe(true)
  })

  it('keeps what the person did when it is not given a value at all', async () => {
    const w = mount(Checkbox, { attrs: { 'aria-label': 'free' } })
    await input(w).setValue(true)
    await flushPromises()
    expect((input(w).element as HTMLInputElement).checked).toBe(true)
  })

  it('does not change when it is disabled', async () => {
    const w = mount(Checkbox, { props: { modelValue: false }, attrs: { disabled: true, 'aria-label': 'off' } })
    expect(input(w).attributes('disabled')).toBeDefined()
    expect(w.classes()).toContain('has-disabled:cursor-not-allowed')
  })
})
