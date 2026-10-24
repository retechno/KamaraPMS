import { describe, expect, it } from 'vitest'
import type { BanquetCalendarSession } from '@/api/types'
import { formatHoldRange, geometry, heldSpan, hhmm, layoutLanes, lockSpan, locksOf, minutesOf, sessionsOf, toneOf, weekDays, weekStart } from './banquet'

const s = (over: Partial<BanquetCalendarSession> = {}): BanquetCalendarSession => ({
  session_id: 1, event_id: 1, event_number: 'BQ-000001', event_name: 'Wedding', event_status: 'DEFINITE', event_type_name: 'Wedding', booker_name: 'A', sales_user_id: 1, option_until: null, option_overdue: false,
  session_name: 'Dinner', session_date: '2026-11-05', start_time: '18:00', end_time: '22:00', ends_next_day: false, room_id: 1, room_code: 'A', room_name: 'Saraswati A', setup_minutes: 60, teardown_minutes: 30,
  setup_style: null, pax_estimated: 100, rank: null, room_taken: false, ...over,
})

describe('the time axis', () => {
  it('reads a time of day', () => {
    expect(minutesOf('09:30:00')).toBe(570)
    expect(minutesOf(null)).toBe(0)
    expect(hhmm(1500)).toBe('01:00')
  })

  it('holds the room from the set-up to the tear-down, and past midnight', () => {
    expect(heldSpan(s())).toEqual({ start: 17 * 60, end: 22 * 60 + 30 })
    expect(heldSpan(s({ start_time: '20:00', end_time: '01:00', ends_next_day: true }))).toEqual({ start: 19 * 60, end: 25 * 60 + 30 })
  })

  it('draws the buffers as parts of the block, cut at the edge of the grid', () => {
    const g = geometry(s(), 6 * 60, 24 * 60)
    expect(g.top).toBeCloseTo((11 / 18) * 100)
    expect(g.setupHeight).toBeCloseTo((60 / 1080) * 100)
    expect(g.teardownHeight).toBeCloseTo((30 / 1080) * 100)
    const late = geometry(s({ start_time: '20:00', end_time: '01:00', ends_next_day: true }), 6 * 60, 24 * 60)
    expect(late.top + late.height).toBeCloseTo(100)
  })
})

describe('the colour of a session', () => {
  it('tells definite, the first option, an option in the queue and an option under a confirmed event', () => {
    expect(toneOf(s())).toBe('definite')
    expect(toneOf(s({ event_status: 'TENTATIVE', rank: 1 }))).toBe('first')
    expect(toneOf(s({ event_status: 'TENTATIVE', rank: 2 }))).toBe('queued')
    expect(toneOf(s({ event_status: 'TENTATIVE', rank: 1, room_taken: true }))).toBe('taken')
  })
})

describe('the week', () => {
  it('starts on Monday', () => {
    expect(weekStart('2026-10-22')).toBe('2026-10-19') // a Thursday
    expect(weekStart('2026-10-25')).toBe('2026-10-19') // a Sunday
    expect(weekDays('2026-10-19')).toHaveLength(7)
    expect(weekDays('2026-10-19')[6]).toBe('2026-10-25')
  })

  it('lists the sessions of a room on a day by start', () => {
    const list = [s({ session_id: 2, start_time: '19:00' }), s({ session_id: 1, start_time: '09:00' }), s({ session_id: 3, room_id: 2 })]
    expect(sessionsOf(list, 1, '2026-11-05').map((x) => x.session_id)).toEqual([1, 2])
  })
})

describe('the locks', () => {
  const hold = (from: string, to: string, over: Record<string, unknown> = {}) => ({ room_id: 1, session_id: 1, hold_from: from, hold_to: to, ...over }) as never

  it('takes the part of a hold that falls on a day, in minutes from its midnight', () => {
    expect(lockSpan(hold('2026-11-05T17:00', '2026-11-05T23:30'), '2026-11-05')).toEqual({ start: 1020, end: 1410 })
    expect(lockSpan(hold('2026-11-05T17:00', '2026-11-05T23:30'), '2026-11-06')).toBeNull()
    expect(lockSpan(hold('2026-11-05T17:00', '2026-11-05T23:30'), '2026-11-04')).toBeNull()
  })

  it('cuts a hold that passes midnight at the end of the day and continues it on the next', () => {
    const late = hold('2026-11-05T21:00', '2026-11-06T01:30')
    expect(lockSpan(late, '2026-11-05')).toEqual({ start: 1260, end: 1440 })
    expect(lockSpan(late, '2026-11-06')).toEqual({ start: 0, end: 90 })
    // a hold that ends at midnight sharp does not touch the next day
    expect(lockSpan(hold('2026-11-05T21:00', '2026-11-06T00:00'), '2026-11-06')).toBeNull()
  })

  it('lists the locks of a room on a day by start', () => {
    const list = [hold('2026-11-05T19:00', '2026-11-05T20:00', { session_id: 2 }), hold('2026-11-05T09:00', '2026-11-05T10:00'), hold('2026-11-05T09:00', '2026-11-05T10:00', { room_id: 2, session_id: 3 })]
    expect(locksOf(list, 1, '2026-11-05').map((x) => x.session_id)).toEqual([1, 2])
  })
})

describe('the lanes', () => {
  const lanes = (items: [number, number, number][]) => Object.fromEntries([...layoutLanes(items.map(([id, start, end]) => ({ id, start, end }))).entries()])

  it('gives an item alone the whole width', () => {
    expect(lanes([[1, 60, 120]])).toEqual({ 1: { lane: 0, lanes: 1 } })
  })

  it('puts two that overlap side by side and lets a later one reuse a free lane', () => {
    expect(lanes([[1, 60, 180], [2, 120, 240], [3, 200, 300]])).toEqual({ 1: { lane: 0, lanes: 2 }, 2: { lane: 1, lanes: 2 }, 3: { lane: 0, lanes: 2 } })
  })

  it('does not narrow an item that starts when another ends, nor one in a separate run', () => {
    expect(lanes([[1, 60, 120], [2, 120, 180], [3, 600, 660]])).toEqual({ 1: { lane: 0, lanes: 1 }, 2: { lane: 0, lanes: 1 }, 3: { lane: 0, lanes: 1 } })
  })

  it('shares the width of a run between as many lanes as it needs', () => {
    expect(lanes([[1, 60, 300], [2, 90, 150], [3, 100, 160]])).toEqual({ 1: { lane: 0, lanes: 3 }, 2: { lane: 1, lanes: 3 }, 3: { lane: 2, lanes: 3 } })
  })
})

describe('the range of a hold', () => {
  it('shows the day and the hours, and both days when it passes midnight', () => {
    expect(formatHoldRange('2026-10-23T09:30', '2026-10-23T15:15')).toMatch(/^\d+ \S+ 09:30–15:15$/)
    const two = formatHoldRange('2026-10-23T22:00', '2026-10-24T02:00')
    expect(two).toMatch(/^\d+ \S+ 22:00 – \d+ \S+ 02:00$/)
    expect(two).not.toContain('T')
  })
})
