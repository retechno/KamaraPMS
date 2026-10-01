import type {
  ArrivalsReport, CashierReport, DailySummaryReport, RevenueReport, StatisticsReport, StayListReport, TaxReport,
} from '@/api/types'

/** A report as the screen shows it: a title, the inputs it needs, and how its answer becomes a table. */
export type ReportKey = 'revenue' | 'tax' | 'cashier' | 'statistics' | 'daily-summary' | 'arrivals' | 'departures' | 'in-house'

export interface Table {
  columns: string[]
  rows: string[][]
  /** Columns that hold amounts or counts (right aligned). */
  numeric: number[]
  footer?: string[]
  note?: string
}

export interface ReportDef {
  key: ReportKey
  title: string
  input: 'range' | 'date' | 'none'
  hint: string
  table: (data: never) => Table
}

const s = (v: unknown): string => (v === null || v === undefined ? '' : String(v))

export const reports: ReportDef[] = [
  {
    key: 'daily-summary', title: 'Daily summary', input: 'date', hint: 'The closing summary of a business date (live for the open day).',
    table: (d: DailySummaryReport) => {
      const sum = d.summary
      if (!sum) return { columns: ['Business date', 'Status'], rows: [[d.business_date, d.status]], numeric: [], note: 'This day has no stored summary.' }
      return {
        columns: ['Measure', 'Value'], numeric: [1], note: d.live ? 'The business day is still open: figures are live.' : undefined,
        rows: [
          ['Business date', d.business_date], ['Status', d.status],
          ['Rooms (total / out of order / out of service)', `${sum.rooms.total} / ${sum.rooms.out_of_order} / ${sum.rooms.out_of_service}`],
          ['Occupied rooms', s(sum.rooms.occupied)], ['Room nights sold', s(sum.rooms.sold)],
          ['Arrivals / departures / no-shows', `${sum.arrivals} / ${sum.departures} / ${sum.no_shows}`],
          ['Room revenue (net)', sum.room_revenue.net], ['Service charge', sum.room_revenue.service], ['Tax', sum.room_revenue.tax],
          ['Occupancy %', sum.occupancy_percent], ['ADR', sum.adr], ['RevPAR', sum.revpar],
          ...sum.payments_by_method.map((m) => [`Payments ${m.method} (net)`, m.net]),
        ],
      }
    },
  },
  {
    key: 'revenue', title: 'Revenue', input: 'range', hint: 'By charge code and revenue account; corrections net out.',
    table: (d: RevenueReport) => ({
      columns: ['Type', 'Charge code', 'Account', 'Items', 'Net', 'Service', 'Tax', 'Total'], numeric: [3, 4, 5, 6, 7],
      rows: d.by_charge_code.map((l) => [l.charge_type, `${l.charge_code} · ${l.name}`, s(l.revenue_account_code), s(l.items), l.net_amount, l.service_charge, l.tax, l.total]),
      footer: ['Total', '', '', s(d.totals.items), d.totals.net_amount, d.totals.service_charge, d.totals.tax, d.totals.total],
    }),
  },
  {
    key: 'tax', title: 'Tax and service charges', input: 'range', hint: 'As posted: a rate edited in the range shows as its own line.',
    table: (d: TaxReport) => ({
      columns: ['Type', 'Code', 'Name', 'Rate %', 'Account', 'Items', 'Base', 'Amount'], numeric: [3, 5, 6, 7],
      rows: [...d.taxes, ...d.service_charges].map((l) => [l.component_type === 'TAX' ? 'Tax' : 'Service', l.code, l.name, l.rate, s(l.gl_account_code), s(l.items), l.base_amount, l.amount]),
      footer: ['Tax total', '', '', '', '', '', '', d.tax_total],
      note: `Service charge total ${d.service_charge_total}.`,
    }),
  },
  {
    key: 'cashier', title: 'Cashier', input: 'range', hint: 'Payments by business date and method; voids are shown apart.',
    table: (d: CashierReport) => ({
      columns: ['Business date', 'Method', 'Payments', 'Refunds', 'Net', 'Count', 'Voided'], numeric: [2, 3, 4, 5, 6],
      rows: d.lines.map((l) => [l.business_date, l.payment_method, l.payments, l.refunds, l.net, s(l.count), `${l.voided} (${l.voided_count})`]),
      footer: ['Net', '', '', '', d.net, '', ''],
    }),
  },
  {
    key: 'statistics', title: 'Occupancy and statistics', input: 'range', hint: 'Closed business days only.',
    table: (d: StatisticsReport) => ({
      columns: ['Business date', 'Rooms', 'Occupied', 'Sold', 'Arrivals', 'Departures', 'No-shows', 'Room revenue', 'Occ %', 'ADR', 'RevPAR'], numeric: [1, 2, 3, 4, 5, 6, 7, 8, 9, 10],
      rows: d.days.map((x) => [x.business_date, `${x.rooms_total - x.rooms_out_of_order}`, s(x.rooms_occupied), s(x.room_nights_sold), s(x.arrivals), s(x.departures), s(x.no_shows), x.room_revenue, x.occupancy_percent, x.adr, x.revpar]),
      footer: ['Total', s(d.totals.available_room_nights), s(d.totals.occupied_room_nights), s(d.totals.room_nights_sold), '', '', '', d.totals.room_revenue, d.totals.occupancy_percent, d.totals.adr, d.totals.revpar],
    }),
  },
  {
    key: 'arrivals', title: 'Arrivals', input: 'date', hint: 'Every room arriving on the date, whatever became of it.',
    table: (d: ArrivalsReport) => ({
      columns: ['Reservation', 'Status', 'Guest', 'Room type', 'Room', 'Departure', 'Party'], numeric: [],
      rows: d.rows.map((a) => [a.confirmation_number, a.status, a.guest, a.room_type, s(a.room), a.departure_date, `${a.adult_count}+${a.child_count}`]),
    }),
  },
  {
    key: 'departures', title: 'Departures', input: 'date', hint: 'Stays leaving on the date, open or checked out.',
    table: (d: StayListReport) => ({
      columns: ['Stay', 'Status', 'Guest', 'Room', 'Arrival', 'Departure', 'Balance'], numeric: [6],
      rows: d.rows.map((x) => [x.stay_number, s(x.status), x.guest, s(x.room), x.arrival_date, x.departure_date, x.balance]),
    }),
  },
  {
    key: 'in-house', title: 'In-house', input: 'none', hint: 'Open stays with their folio balance.',
    table: (d: StayListReport) => ({
      columns: ['Stay', 'Guest', 'Room', 'Arrival', 'Departure', 'Party', 'Balance'], numeric: [6],
      rows: d.rows.map((x) => [x.stay_number, x.guest, s(x.room), x.arrival_date, x.departure_date, `${x.adult_count}+${x.child_count}`, x.balance]),
    }),
  },
]
