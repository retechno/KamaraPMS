import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import NightsChart from './NightsChart.vue'

const bars = [
  { key: '2026-10-10', percent: 78, label: '10 Oct', short: '10' },
  { key: '2026-10-11', percent: 95, label: '11 Oct', short: '11' },
  { key: '2026-10-12', percent: 30, label: '12 Oct' },
]

describe('NightsChart', () => {
  it('draws a bar for each night, as high as its percent, and the lines at 50% and 100%', () => {
    const w = mount(NightsChart, { props: { bars, testPrefix: 'n' } })
    expect(w.findAll('[data-testid=n-bar]').map((b) => b.attributes('style'))).toEqual(['height: 78%;', 'height: 95%;', 'height: 30%;'])
    expect(w.find('[data-slot=line-100]').exists()).toBe(true)
    expect(w.find('[data-slot=line-50]').exists()).toBe(true)
  })

  it('has the strong colour from 90% (or the percent it is told) and not under', () => {
    const w = mount(NightsChart, { props: { bars, testPrefix: 'n' } })
    expect(w.findAll('[data-testid=n-bar]').map((b) => b.attributes('data-strong'))).toEqual(['false', 'true', 'false'])
    const lower = mount(NightsChart, { props: { bars, strongFrom: 75, testPrefix: 'n' } })
    expect(lower.findAll('[data-testid=n-bar]').map((b) => b.attributes('data-strong'))).toEqual(['true', 'true', 'false'])
  })

  it('draws a percent over 100 as 100', () => {
    const w = mount(NightsChart, { props: { bars: [{ key: 'a', percent: 140, label: 'x' }], testPrefix: 'n' } })
    expect(w.get('[data-testid=n-bar]').attributes('style')).toContain('height: 100%')
  })

  it('puts the day alone under the bar on a phone and the day with the month from a tablet up, one line either way', () => {
    const w = mount(NightsChart, { props: { bars, testPrefix: 'n' } })
    const first = w.findAll('[data-testid=n-day]')[0]!
    expect(first.get('[data-slot=label-short]').text()).toBe('10')
    expect(first.get('[data-slot=label-short]').classes()).toContain('sm:hidden')
    expect(first.get('[data-slot=label-long]').text()).toBe('10 Oct')
    expect(first.get('[data-slot=label-long]').classes()).toContain('hidden')
    expect(first.get('small').classes()).toContain('whitespace-nowrap') // it never goes to a second line
  })

  it('has one label when it has no short one', () => {
    const w = mount(NightsChart, { props: { bars, testPrefix: 'n' } })
    const last = w.findAll('[data-testid=n-day]')[2]!
    expect(last.find('[data-slot=label-short]').exists()).toBe(false)
    expect(last.text()).toBe('12 Oct')
  })
})
