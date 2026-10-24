import { describe, expect, it } from 'vitest'
import type { BanquetCalendarSession } from '@/api/types'
import { geometry, heldSpan, hhmm, minutesOf, sessionsOf, toneOf, weekDays, weekStart } from './banquet'

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
