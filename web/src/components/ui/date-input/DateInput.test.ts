import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, ref } from 'vue'
import { setLocale } from '@/i18n'
import DateInput from './DateInput.vue'

const mounted: VueWrapper[] = []

function mountField(attrs: Record<string, unknown> = {}, initial = '2026-10-01') {
  const changes: string[] = []
  const Host = defineComponent({
    components: { DateInput },
    setup: () => ({ value: ref(initial), attrs, onChange: (v: string) => changes.push(v) }),
    template: '<DateInput id="d" v-model="value" name="arrival" v-bind="attrs" @change="onChange" /><output data-testid="model">{{ value }}</output>',
  })
  const wrapper = mount(Host, { attachTo: document.body })
  mounted.push(wrapper)
  const text = () => wrapper.get('input#d')
  const native = () => wrapper.get('input[name=arrival]')
  const model = () => wrapper.get('[data-testid=model]').text()
  return { wrapper, changes, text, native, model }
}

describe('DateInput', () => {
  afterEach(async () => {
    for (const w of mounted.splice(0)) w.unmount()
    document.body.innerHTML = ''
    await setLocale('en')
  })

  it('writes the date as dd/mm/yyyy and keeps the name, the ISO value, min and max on a native field that is only hidden', () => {
    const { text, native } = mountField({ min: '2026-09-01', max: '2026-12-31' }, '2026-11-02')
    expect((text().element as HTMLInputElement).value).toBe('02/11/2026')
    expect(text().attributes('name')).toBeUndefined()
    expect(text().attributes('type')).toBe('text')
    expect(text().attributes('inputmode')).toBe('numeric')
    const n = native().element as HTMLInputElement
    expect(n.type).toBe('date')
    expect(n.value).toBe('2026-11-02')
    expect(n.min).toBe('2026-09-01')
    expect(n.max).toBe('2026-12-31')
    expect(native().attributes('tabindex')).toBe('-1')
    expect(native().attributes('aria-hidden')).toBe('true')
  })

  it('takes a whole typed date and emits the ISO date and one change', async () => {
    const { text, native, model, changes } = mountField()
    await text().setValue('02/11/2026')
    expect(model()).toBe('2026-11-02')
    expect((native().element as HTMLInputElement).value).toBe('2026-11-02')
    expect(changes).toEqual(['2026-11-02'])
  })

  it('puts the slashes while digits are typed', async () => {
    const { text, model } = mountField()
    await text().setValue('02112026')
    expect((text().element as HTMLInputElement).value).toBe('02/11/2026')
    expect(model()).toBe('2026-11-02')
  })

  it('reads a pasted ISO date and a date with dashes', async () => {
    const { text, model } = mountField()
    await text().setValue('2026-11-05')
    expect(model()).toBe('2026-11-05')
    await text().setValue('7-11-2026')
    expect(model()).toBe('2026-11-07')
  })

  it('does not change or emit anything while the date is half typed, and says so only when the field is left', async () => {
    const { text, model, changes } = mountField()
    await text().setValue('02/11/20')
    expect(model()).toBe('2026-10-01')
    expect(changes).toEqual([])
    expect(text().attributes('aria-invalid')).toBeUndefined()
    await text().trigger('blur')
    expect(text().attributes('aria-invalid')).toBe('true')
    expect(document.body.textContent).toContain('Write the date as dd/mm/yyyy, for example 02/11/2026.')
    expect((text().element as HTMLInputElement).value).toBe('02/11/20') // the text stays
    expect(model()).toBe('2026-10-01') // the model keeps the last valid date
    expect(changes).toEqual([])
  })

  it('refuses a date that does not exist (31/02/2026): the text stays, the field is invalid, the model keeps its date', async () => {
    const { text, model, changes } = mountField()
    await text().setValue('31/02/2026')
    await text().trigger('blur')
    expect(model()).toBe('2026-10-01')
    expect(changes).toEqual([])
    expect(text().attributes('aria-invalid')).toBe('true')
    expect((text().element as HTMLInputElement).value).toBe('31/02/2026')
    expect(text().attributes('aria-describedby')).toBeTruthy()
    // Typing a real date again clears the message.
    await text().setValue('28/02/2027')
    expect(model()).toBe('2027-02-28')
    expect(text().attributes('aria-invalid')).toBeUndefined()
    expect(document.body.querySelector('[data-slot=date-input-problem]')).toBeNull()
  })

  it('says that a date is before min or after max, and does not take it', async () => {
    const { text, model, changes } = mountField({ min: '2026-10-01', max: '2026-12-31' })
    await text().setValue('30/09/2026')
    expect(document.body.textContent).toContain('The earliest date is 01/10/2026.')
    expect(text().attributes('aria-invalid')).toBe('true')
    await text().setValue('01/01/2027')
    expect(document.body.textContent).toContain('The latest date is 31/12/2026.')
    expect(model()).toBe('2026-10-01')
    expect(changes).toEqual([])
    await text().setValue('31/12/2026')
    expect(model()).toBe('2026-12-31')
    expect(text().attributes('aria-invalid')).toBeUndefined()
  })

  it('writes its messages in Indonesian', async () => {
    await setLocale('id')
    const { text } = mountField({ min: '2026-10-01' })
    await text().setValue('30/09/2026')
    expect(document.body.textContent).toContain('Tanggal paling awal 01/10/2026.')
    expect(text().attributes('placeholder')).toBe('hh/bb/tttt')
  })

  it('emits an empty value when the field is emptied', async () => {
    const { text, model, changes } = mountField()
    await text().setValue('')
    expect(model()).toBe('')
    expect(changes).toEqual([''])
  })

  it('follows a value written into the native field (a click in the calendar, a test) and emits change on its change event, as a native input did', async () => {
    const { text, native, model, changes } = mountField()
    // The "input" event alone moves the value; "change" says it was chosen. (setValue of Vue Test Utils sends both.)
    const n = native().element as HTMLInputElement
    n.value = '2026-10-04'
    await native().trigger('input')
    expect(model()).toBe('2026-10-04')
    expect((text().element as HTMLInputElement).value).toBe('04/10/2026')
    expect(changes).toEqual([])
    await native().trigger('change')
    expect(changes).toEqual(['2026-10-04'])
    await native().setValue('2026-10-05')
    expect(model()).toBe('2026-10-05')
    expect(n.value).toBe('2026-10-05')
    expect((text().element as HTMLInputElement).value).toBe('05/10/2026')
    expect(changes).toEqual(['2026-10-04', '2026-10-05'])
  })

  it('rewrites the text when the page changes the value', async () => {
    const { wrapper, text, native } = mountField({}, '2026-10-01')
    await text().setValue('31/02')
    ;(wrapper.vm as unknown as { value: string }).value = '2026-12-24'
    await flushPromises()
    expect((text().element as HTMLInputElement).value).toBe('24/12/2026')
    expect((native().element as HTMLInputElement).value).toBe('2026-12-24')
    ;(wrapper.vm as unknown as { value: string }).value = ''
    await flushPromises()
    expect((text().element as HTMLInputElement).value).toBe('')
  })

  it('shows the weekday of the understood date under the field, in the language of the page', async () => {
    const { text } = mountField({}, '2026-11-02')
    expect(document.body.querySelector('[data-slot=date-input-weekday]')?.textContent).toBe('Mon, 2 Nov 2026')
    await setLocale('id')
    await text().setValue('03/11/2026')
    await flushPromises()
    expect(document.body.querySelector('[data-slot=date-input-weekday]')?.textContent).toBe('Sel, 3 Nov 2026')
    await text().setValue('03/11/20')
    expect(document.body.querySelector('[data-slot=date-input-weekday]')).toBeNull()
  })

  it('opens the calendar of the system with showPicker, and focuses and clicks the field when showPicker is not there', async () => {
    const { wrapper, native } = mountField()
    const button = wrapper.get('button[data-slot=date-input-calendar]')
    expect(button.attributes('aria-label')).toBe('Open the calendar')
    const n = native().element as HTMLInputElement
    const showPicker = vi.fn()
    ;(n as unknown as { showPicker: () => void }).showPicker = showPicker
    await button.trigger('click')
    expect(showPicker).toHaveBeenCalledTimes(1)

    ;(n as unknown as { showPicker: () => void }).showPicker = () => {
      throw new Error('NotAllowedError')
    }
    const click = vi.spyOn(n, 'click')
    const focus = vi.spyOn(n, 'focus')
    await button.trigger('click')
    expect(focus).toHaveBeenCalled()
    expect(click).toHaveBeenCalled()
  })

  it('keeps the id, the aria attributes and data attributes of the page on the text field', () => {
    const { text } = mountField({ 'aria-describedby': 'hint-1', 'data-testid': 'arrival-field', required: true })
    expect(text().attributes('aria-describedby')).toBe('hint-1')
    expect(text().attributes('data-testid')).toBe('arrival-field')
    expect(text().attributes('required')).toBeDefined()
  })

  it('is invalid when the page says so, and then writes no weekday', () => {
    const { text } = mountField({ 'aria-invalid': true }, '2026-11-02')
    expect(text().attributes('aria-invalid')).toBe('true')
    expect(document.body.querySelector('[data-slot=date-input-weekday]')).toBeNull()
  })

  it('is disabled as a whole', () => {
    const { wrapper, text, native } = mountField({ disabled: true })
    expect(text().attributes('disabled')).toBeDefined()
    expect(native().attributes('disabled')).toBeDefined()
    expect(wrapper.get('button[data-slot=date-input-calendar]').attributes('disabled')).toBeDefined()
  })
})
