import { describe, expect, it } from 'vitest'
import { blockColumns, windowDates } from './blocks'

const start = '2026-10-01'

describe('blockColumns', () => {
  it('places a block inside the window (end date is exclusive)', () => {
    expect(blockColumns({ start_date: '2026-10-03', end_date: '2026-10-05' }, start, 14)).toEqual({ start: 3, end: 5 })
  })

  it('clips blocks that begin before or end after the window', () => {
    expect(blockColumns({ start_date: '2026-09-25', end_date: '2026-10-03' }, start, 14)).toEqual({ start: 1, end: 3 })
    expect(blockColumns({ start_date: '2026-10-10', end_date: '2026-11-30' }, start, 14)).toEqual({ start: 10, end: 15 })
  })

  it('ignores blocks that only touch the window edges', () => {
    expect(blockColumns({ start_date: '2026-09-28', end_date: '2026-10-01' }, start, 14)).toBeNull()
    expect(blockColumns({ start_date: '2026-10-15', end_date: '2026-10-16' }, start, 14)).toBeNull()
  })

  it('works across month and year ends', () => {
    expect(blockColumns({ start_date: '2026-12-31', end_date: '2027-01-02' }, '2026-12-30', 7)).toEqual({ start: 2, end: 4 })
  })
})

describe('windowDates', () => {
  it('lists consecutive dates', () => {
    expect(windowDates('2026-02-27', 3)).toEqual(['2026-02-27', '2026-02-28', '2026-03-01'])
  })
})
