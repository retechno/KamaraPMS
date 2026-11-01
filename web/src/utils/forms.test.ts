import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, ref } from 'vue'
import DateInput from '@/components/ui/date-input/DateInput.vue'
import { guardForm, hasInvalidDate } from './forms'

const mounted: { unmount: () => void }[] = []

function mountForm() {
  const sent = vi.fn()
  const Host = defineComponent({
    components: { DateInput },
    setup: () => ({ a: ref('2026-10-01'), b: ref('2026-10-03'), c: ref(''), guardForm, sent }),
    template: `
      <form ref="f" novalidate data-testid="f" @submit.prevent="guardForm($event) && sent()">
        <DateInput id="a" v-model="a" name="a" /><DateInput id="b" v-model="b" name="b" min="2026-10-01" />
        <input id="other" name="other" type="number" min="5" value="1" /><button type="submit">go</button>
      </form>
      <form novalidate data-testid="g" @submit.prevent="guardForm($event, $refs.f) && sent()"><DateInput id="c" v-model="c" name="c" /></form>`,
  })
  const w = mount(Host, { attachTo: document.body })
  mounted.push(w)
  const text = (id: string) => w.get(`input#${id}`)
  return { w, sent, text }
}

describe('guardForm', () => {
  afterEach(() => {
    for (const w of mounted.splice(0)) w.unmount()
    document.body.innerHTML = ''
  })

  it('lets a form go when its dates are real, and does not judge the other fields (a number below its min is the business of the server)', async () => {
    const { w, sent } = mountForm()
    await w.get('form[data-testid=f]').trigger('submit')
    expect(sent).toHaveBeenCalledTimes(1)
  })

  it('stops a form whose date is not on the calendar, says why and puts the cursor on that field', async () => {
    const { w, sent, text } = mountForm()
    await text('b').setValue('31/02/2026')
    await w.get('form[data-testid=f]').trigger('submit')
    expect(sent).not.toHaveBeenCalled()
    expect(document.activeElement).toBe(text('b').element)
    expect(text('b').attributes('aria-invalid')).toBe('true')
    expect(document.body.textContent).toContain('31/02/2026 is not on the calendar')
  })

  it('stops a form with a date half typed, and goes to the first field that is not valid', async () => {
    const { w, sent, text } = mountForm()
    await text('b').setValue('02/1')
    await text('a').setValue('3/10')
    await w.get('form[data-testid=f]').trigger('submit')
    expect(sent).not.toHaveBeenCalled()
    expect(document.activeElement).toBe(text('a').element)
    await flushPromises()
    expect(document.body.textContent).toContain('Write as dd/mm/yyyy')
  })

  it('stops a form with a date outside min, and goes on again when the date is corrected', async () => {
    const { w, sent, text } = mountForm()
    await text('b').setValue('30/09/2026')
    await w.get('form[data-testid=f]').trigger('submit')
    expect(sent).not.toHaveBeenCalled()
    await text('b').setValue('05/10/2026')
    await w.get('form[data-testid=f]').trigger('submit')
    expect(sent).toHaveBeenCalledTimes(1)
  })

  it('also looks at the date fields of another form that this one sends (the booking sends what the search holds)', async () => {
    const { w, sent, text } = mountForm()
    await text('a').setValue('31/02/2026')
    await w.get('form[data-testid=g]').trigger('submit')
    expect(sent).not.toHaveBeenCalled()
    expect(document.activeElement).toBe(text('a').element)
  })

  it('tells whether a place holds a date that is not valid', async () => {
    const { w, text } = mountForm()
    const form = w.get('form[data-testid=f]').element
    expect(hasInvalidDate(form)).toBe(false)
    await text('a').setValue('31/02')
    expect(hasInvalidDate(form)).toBe(true)
    expect(hasInvalidDate(null)).toBe(false)
  })
})
