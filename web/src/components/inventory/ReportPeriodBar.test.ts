import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, describe, expect, it } from 'vitest'
import { defineComponent, ref } from 'vue'
import { setLocale } from '@/i18n'
import { usePropertyStore } from '@/stores/property'
import ReportPeriodBar from './ReportPeriodBar.vue'

function mountBar(props: Record<string, unknown> = {}) {
  const pinia = createPinia()
  setActivePinia(pinia)
  usePropertyStore().clock = { business_date: '2026-09-30' } as never
  const shown: number[] = []
  const Host = defineComponent({
    components: { ReportPeriodBar },
    setup: () => ({ from: ref('2026-09-01'), to: ref('2026-09-30'), props, show: () => shown.push(1) }),
    template: '<ReportPeriodBar v-model:from="from" v-model:to="to" v-bind="props" @show="show" /><output data-testid="range">{{ from }}|{{ to }}</output>',
  })
  const w = mount(Host, { global: { plugins: [pinia] }, attachTo: document.body })
  return { w, shown, range: () => w.get('[data-testid=range]').text() }
}

describe('ReportPeriodBar', () => {
  afterEach(async () => {
    document.body.innerHTML = ''
    await setLocale('en')
  })

  it('writes the period as dd/mm/yyyy and keeps the name and the ISO date on the hidden native fields', () => {
    const { w } = mountBar()
    const text = w.findAll('input[data-slot=date-input-text]').map((i) => (i.element as HTMLInputElement).value)
    expect(text).toEqual(['01/09/2026', '30/09/2026'])
    expect((w.get('input[name=from]').element as HTMLInputElement).value).toBe('2026-09-01')
    expect((w.get('input[name=to]').element as HTMLInputElement).value).toBe('2026-09-30')
  })

  it('gives the show button and the period picks one height', () => {
    const { w } = mountBar()
    const show = w.get('[data-testid=report-show]').classes()
    const picks = w.findAll('[data-testid^=pick-]')
    expect(picks.length).toBeGreaterThan(0)
    for (const pick of picks) {
      expect(pick.classes()).toContain('h-9')
      expect(pick.classes()).not.toContain('h-8')
    }
    expect(show).toContain('h-9')
  })

  it('shows the report for a whole period, and not while a date is half typed', async () => {
    const { w, shown } = mountBar()
    await w.get('[data-testid=report-period]').trigger('submit')
    expect(shown).toHaveLength(1)
    await w.findAll('input[data-slot=date-input-text]')[1]!.setValue('3/9')
    await w.get('[data-testid=report-period]').trigger('submit')
    await flushPromises()
    expect(shown).toHaveLength(1)
    expect(w.text()).toContain('Write as dd/mm/yyyy')
  })

  it('takes a period from the quick picks, rewrites both texts and shows the report', async () => {
    const { w, shown, range } = mountBar()
    await w.get('[data-testid=pick-last-month]').trigger('click')
    await flushPromises()
    expect(range()).toBe('2026-08-01|2026-08-31')
    expect(w.findAll('input[data-slot=date-input-text]').map((i) => (i.element as HTMLInputElement).value)).toEqual(['01/08/2026', '31/08/2026'])
    expect(shown).toHaveLength(1)
  })

  it('has one date of its own when the report is as of one day', () => {
    const { w } = mountBar({ single: true })
    expect(w.findAll('input[data-slot=date-input-text]')).toHaveLength(1)
    expect(w.find('input[name=from]').exists()).toBe(false)
  })
})
