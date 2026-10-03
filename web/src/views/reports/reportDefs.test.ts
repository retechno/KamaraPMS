import { describe, expect, it } from 'vitest'
import { setLocale } from '@/i18n'
import { reports } from './reportDefs'

describe('reportDefs', () => {
  it('has a title, a hint and column names in both languages', () => {
    try {
      for (const locale of ['en', 'id'] as const) {
        setLocale(locale)
        for (const def of reports) {
          expect(def.title()).not.toContain('reportDefs.')
          expect(def.hint()).not.toContain('reportDefs.')
        }
      }
      setLocale('id')
      expect(reports.find((x) => x.key === 'maintenance')?.title()).toBe('Pemeliharaan')
      const cashier = reports.find((x) => x.key === 'cashier')!
      const table = cashier.table({ lines: [], net: '0' } as never)
      expect(table.columns[0]).toBe('Tanggal bisnis')
      expect(table.footer?.[0]).toBe('Bersih')
    } finally {
      setLocale('en')
    }
  })

  it('shows the complimentary and house use rooms in the statistics and the kind in the lists', () => {
    const stats = reports.find((x) => x.key === 'statistics')!.table({
      days: [{ business_date: '2026-09-30', rooms_total: 10, rooms_out_of_order: 1, rooms_house_use: 1, rooms_occupied: 6, rooms_complimentary: 2, room_nights_sold: 4, arrivals: 3, departures: 2, no_shows: 0, room_revenue: '4000000', occupancy_percent: '75.00', adr: '1000000', revpar: '500000' }],
      totals: { available_room_nights: 8, occupied_room_nights: 6, complimentary_room_nights: 2, house_use_room_nights: 1, room_nights_sold: 4, room_revenue: '4000000', occupancy_percent: '75.00', adr: '1000000', revpar: '500000' },
    } as never)
    expect(stats.columns.slice(2, 6)).toEqual(['Occupied', 'Compl.', 'House use', 'Sold (paid)'])
    expect(stats.rows[0]?.slice(1, 6)).toEqual(['8', '6', '2', '1', '4']) // available rooms leave out out-of-order and house use rooms
    expect(stats.footer?.slice(1, 6)).toEqual(['8', '6', '2', '1', '4'])

    const inHouse = reports.find((x) => x.key === 'in-house')!.table({
      rows: [
        { stay_number: 'S1', guest: 'A', room: '101', arrival_date: '2026-09-30', departure_date: '2026-10-01', adult_count: 1, child_count: 0, balance: '0', occupancy_kind: 'PAID' },
        { stay_number: 'S2', guest: 'B', room: '102', arrival_date: '2026-09-30', departure_date: '2026-10-01', adult_count: 1, child_count: 0, balance: '0', occupancy_kind: 'COMPLIMENTARY' },
      ],
    } as never)
    expect(inHouse.rows.map((x) => x[7])).toEqual(['', 'Complimentary'])
  })

  it('puts the free rooms in three tables and says what the value rests on', () => {
    const data = {
      reference_plan: 'BAR',
      lines: [{ confirmation_number: 'RES1', guest: 'Siti', room_type: 'DLX', room: '102', rate_plan: 'COMP', occupancy_kind: 'COMPLIMENTARY', reason: 'Owner guest', nights: 2, value: '2000000', missing_nights: 0 }],
      by_reason: [{ key: 'Owner guest', rooms: 1, nights: 2, value: '2000000', missing_nights: 0 }],
      by_kind: [{ key: 'COMPLIMENTARY', rooms: 1, nights: 2, value: '2000000', missing_nights: 0 }],
      totals: { rooms: 1, nights: 2, value: '2000000', missing_nights: 1 },
    }
    const list = reports.find((x) => x.key === 'free-rooms')!.table(data as never)
    expect(list.rows[0]).toEqual(['RES1', 'Siti', 'DLX', '102', 'COMP', 'Complimentary', 'Owner guest', '2', '2000000'])
    expect(list.footer?.slice(-2)).toEqual(['2', '2000000'])
    expect(list.note).toContain('BAR')
    expect(list.note).toContain('1 night(s)')
    expect(reports.find((x) => x.key === 'free-rooms-by-reason')!.table(data as never).rows[0]).toEqual(['Owner guest', '1', '2', '2000000'])
    expect(reports.find((x) => x.key === 'free-rooms-by-kind')!.table(data as never).rows[0]?.[0]).toBe('Complimentary')
    const without = reports.find((x) => x.key === 'free-rooms')!.table({ ...data, reference_plan: '', totals: { ...data.totals, missing_nights: 0 } } as never)
    expect(without.note).toContain('no reference rate plan')
  })
})
