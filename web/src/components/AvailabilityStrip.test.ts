import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import { setLocale } from '@/i18n'
import AvailabilityStrip from './AvailabilityStrip.vue'

let GET = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a) } }))

const night = (date: string, available: number) => ({ date, sellable: 3, blocked: 0, held: 3 - available, available, occupancy_percent: 50, in_house: 0, arrivals: 0, reservations: 0, complimentary: 0, house_use: 0 })
const addDays = (d: string, n: number) => new Date(Date.parse(`${d}T00:00:00Z`) + n * 86_400_000).toISOString().slice(0, 10)
function calendarFrom(from: string, perNight: (d: string) => number = () => 2) {
  const dates = Array.from({ length: 14 }, (_, i) => addDays(from, i))
  return { from, to: addDays(from, 14), room_types: [{ room_type_id: 10, code: 'DLX', name: 'Deluxe', rooms_total: 3, nights: dates.map((d) => night(d, perNight(d))) }], totals: [] }
}

const base = { propertyId: 7, arrival: '2026-10-05', departure: '2026-10-07', businessDate: '2026-10-02' }
function mountStrip(props: Partial<typeof base> = {}) {
  return mount(AvailabilityStrip, { props: { ...base, ...props } })
}

describe('AvailabilityStrip', () => {
  beforeEach(() => {
    setLocale('en')
    GET = vi.fn(async (_path: string, opts: { params: { query: { from: string } } }) => ({ data: calendarFrom(opts.params.query.from) }))
  })

  it('asks the calendar for 14 nights from the arrival', async () => {
    const w = mountStrip()
    await flushPromises()
    expect(GET).toHaveBeenCalledTimes(1)
    expect(GET).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/availability/calendar', { params: { path: { propertyId: 7 }, query: { from: '2026-10-05', to: '2026-10-19' } } })
    expect(w.findAll('[data-strip-day]')).toHaveLength(14)
    expect(w.findAll('[data-strip-day]')[0]!.attributes('data-strip-day')).toBe('2026-10-05')
    expect(w.findAll('[data-testid=strip-DLX] [data-strip-cell]')).toHaveLength(14)
  })

  it('shows the figure only, with the figures of the type in the tooltip', async () => {
    const w = mountStrip()
    await flushPromises()
    const cell = w.get('[data-strip-cell="DLX-2026-10-05"]')
    expect(cell.text()).toBe('2')
    expect(cell.attributes('title')).toContain('1 held of 3 sellable')
  })

  it('asks again when the arrival changes, but not when the departure changes', async () => {
    const w = mountStrip()
    await flushPromises()
    await w.setProps({ departure: '2026-10-09' })
    await flushPromises()
    expect(GET).toHaveBeenCalledTimes(1)
    await w.setProps({ arrival: '2026-10-08', departure: '2026-10-10' })
    await flushPromises()
    expect(GET).toHaveBeenCalledTimes(2)
    expect(GET.mock.calls[1]![1].params.query.from).toBe('2026-10-08')
  })

  it('shades the nights of the stay and no other', async () => {
    const w = mountStrip({ arrival: '2026-10-05', departure: '2026-10-07' })
    await flushPromises()
    const shaded = w.findAll('[data-strip-cell]').filter((c) => c.classes().includes('bg-primary/10')).map((c) => c.attributes('data-strip-cell'))
    expect(shaded).toEqual(['DLX-2026-10-05', 'DLX-2026-10-06'])
    const heads = w.findAll('[data-strip-day]').filter((h) => h.classes().includes('border-t-primary')).map((h) => h.attributes('data-strip-day'))
    expect(heads).toEqual(['2026-10-05', '2026-10-06'])
    expect(w.get('[data-strip-day="2026-10-05"]').text()).toContain('(night of the stay)') // not by colour alone
  })

  it('marks the business date in the head', async () => {
    const w = mountStrip({ arrival: '2026-10-02', departure: '2026-10-03' })
    await flushPromises()
    expect(w.get('[data-strip-day="2026-10-02"]').classes()).toContain('text-primary')
    expect(w.get('[data-strip-day="2026-10-03"]').classes()).not.toContain('text-primary')
  })

  it('colours a full type amber and an oversold one red', async () => {
    GET = vi.fn(async (_p: string, opts: { params: { query: { from: string } } }) => ({ data: calendarFrom(opts.params.query.from, (d) => (d === '2026-10-06' ? 0 : d === '2026-10-07' ? -1 : 2)) }))
    const w = mountStrip()
    await flushPromises()
    expect(w.get('[data-strip-cell="DLX-2026-10-06"]').classes()).toContain('bg-warning/20')
    const over = w.get('[data-strip-cell="DLX-2026-10-07"]')
    expect(over.classes()).toContain('text-destructive')
    expect(over.text()).toBe('-1')
    expect(w.get('[data-strip-cell="DLX-2026-10-05"]').classes()).not.toContain('bg-warning/20')
  })

  it('says when the stay is longer than the 14 nights shown', async () => {
    const w = mountStrip({ departure: '2026-10-25' })
    await flushPromises()
    expect(w.text()).toContain('longer than 14 nights')
    await w.setProps({ departure: '2026-10-19' })
    expect(w.text()).not.toContain('longer than 14 nights')
  })

  it('does not ask for an arrival before the business date, or an arrival that is not a date', async () => {
    const w = mountStrip({ arrival: '2026-10-01' })
    await flushPromises()
    await w.setProps({ arrival: '2026-1' })
    await flushPromises()
    expect(GET).not.toHaveBeenCalled()
    expect(w.find('[data-testid=availability-strip]').exists()).toBe(false)
  })

  it('drops the answer of an arrival that has been replaced since', async () => {
    const waiting: Array<() => void> = []
    GET = vi.fn((_p: string, opts: { params: { query: { from: string } } }) => new Promise((resolve) => {
      const from = opts.params.query.from
      waiting.push(() => resolve({ data: calendarFrom(from, () => (from === '2026-10-05' ? 1 : 2)) }))
    }))
    const w = mountStrip()
    await nextTick()
    await w.setProps({ arrival: '2026-10-08', departure: '2026-10-09' })
    await nextTick()
    waiting[1]!() // the later one answers first
    await flushPromises()
    waiting[0]!() // the earlier one answers late
    await flushPromises()
    expect(w.get('[data-strip-day]').attributes('data-strip-day')).toBe('2026-10-08')
    expect(w.get('[data-strip-cell="DLX-2026-10-08"]').text()).toBe('2')
    expect(w.find('[data-strip-cell="DLX-2026-10-05"]').exists()).toBe(false)
  })

  it('says it could not load, and keeps the page', async () => {
    GET = vi.fn().mockRejectedValue(new Error('forbidden'))
    const w = mountStrip()
    await flushPromises()
    expect(w.get('[data-testid=strip-failed]').text()).toContain('could not be loaded')
    expect(w.find('table').exists()).toBe(false)
  })

  it('has its texts in Indonesian', async () => {
    setLocale('id')
    const w = mountStrip()
    await flushPromises()
    expect(w.get('.sr-only').text()).toContain('Kamar tersisa')
  })
})
