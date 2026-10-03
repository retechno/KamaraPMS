import type {
  ArrivalsReport, CashierReport, DailySummaryReport, HousekeepingDirtyRoomsReport, HousekeepingProductivityReport, MaintenanceReport, RevenueReport,
  StatisticsReport, StayListReport, TaxReport,
} from '@/api/types'
import { t } from '@/i18n'

/** A report as the screen shows it: a title, the inputs it needs, and how its answer becomes a table. */
export type ReportKey = 'revenue' | 'tax' | 'cashier' | 'statistics' | 'daily-summary' | 'arrivals' | 'departures' | 'in-house' | 'housekeeping-productivity' | 'housekeeping-dirty-rooms' | 'maintenance'

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
  /** The title and the hint are read when they are shown, so they follow the language of the moment. */
  title: () => string
  input: 'range' | 'date' | 'hours' | 'none'
  hint: () => string
  /** Builds the table in the language of the moment: call it again when the language changes. */
  table: (data: never) => Table
}

const s = (v: unknown): string => (v === null || v === undefined ? '' : String(v))
/** The texts of the reports are `reportDefs.<name>`; the names are typed by the language files. */
const r = (key: string, params?: Record<string, unknown>): string => t(`reportDefs.${key}` as 'reportDefs.total', params as never)

/** The kind of a room in a list: nothing for a paid room, the name for a complimentary or house use one. */
const kind = (k: string): string => (k === 'PAID' ? '' : t(`occupancy.kind_${k}` as 'occupancy.kind_PAID'))

export const reports: ReportDef[] = [
  {
    key: 'daily-summary', title: () => r('dailySummary'), input: 'date', hint: () => r('dailySummaryHint'),
    table: (d: DailySummaryReport) => {
      const sum = d.summary
      if (!sum) return { columns: [r('businessDate'), r('status')], rows: [[d.business_date, d.status]], numeric: [], note: r('noSummary') }
      return {
        columns: [r('measure'), r('value')], numeric: [1], note: d.live ? r('liveNote') : undefined,
        rows: [
          [r('businessDate'), d.business_date], [r('status'), d.status],
          [r('roomsTotals'), `${sum.rooms.total} / ${sum.rooms.out_of_order} / ${sum.rooms.out_of_service}`],
          [r('occupiedRooms'), s(sum.rooms.occupied)], [r('compRooms'), s(sum.rooms.complimentary)], [r('houseUseRooms'), s(sum.rooms.house_use)], [r('roomNightsSold'), s(sum.rooms.sold)],
          [r('arrDepNoShow'), `${sum.arrivals} / ${sum.departures} / ${sum.no_shows}`],
          [r('roomRevenueNet'), sum.room_revenue.net], [r('serviceCharge'), sum.room_revenue.service], [r('tax'), sum.room_revenue.tax],
          [r('occupancyPercent'), sum.occupancy_percent], ['ADR', sum.adr], ['RevPAR', sum.revpar],
          ...sum.payments_by_method.map((m) => [r('paymentsBy', { method: m.method }), m.net]),
        ],
      }
    },
  },
  {
    key: 'revenue', title: () => r('revenue'), input: 'range', hint: () => r('revenueHint'),
    table: (d: RevenueReport) => ({
      columns: [r('type'), r('chargeCode'), r('account'), r('items'), r('net'), r('service'), r('tax'), r('total')], numeric: [3, 4, 5, 6, 7],
      rows: d.by_charge_code.map((l) => [l.charge_type, `${l.charge_code} · ${l.name}`, s(l.revenue_account_code), s(l.items), l.net_amount, l.service_charge, l.tax, l.total]),
      footer: [r('total'), '', '', s(d.totals.items), d.totals.net_amount, d.totals.service_charge, d.totals.tax, d.totals.total],
    }),
  },
  {
    key: 'tax', title: () => r('taxTitle'), input: 'range', hint: () => r('taxHint'),
    table: (d: TaxReport) => ({
      columns: [r('type'), r('code'), r('name'), r('ratePercent'), r('account'), r('items'), r('base'), r('amount')], numeric: [3, 5, 6, 7],
      rows: [...d.taxes, ...d.service_charges].map((l) => [l.component_type === 'TAX' ? r('tax') : r('serviceKind'), l.code, l.name, l.rate, s(l.gl_account_code), s(l.items), l.base_amount, l.amount]),
      footer: [r('taxTotal'), '', '', '', '', '', '', d.tax_total],
      note: r('serviceTotalNote', { amount: d.service_charge_total }),
    }),
  },
  {
    key: 'cashier', title: () => r('cashier'), input: 'range', hint: () => r('cashierHint'),
    table: (d: CashierReport) => ({
      columns: [r('businessDate'), r('method'), r('payments'), r('refunds'), r('net'), r('count'), r('voided')], numeric: [2, 3, 4, 5, 6],
      rows: d.lines.map((l) => [l.business_date, l.payment_method, l.payments, l.refunds, l.net, s(l.count), `${l.voided} (${l.voided_count})`]),
      footer: [r('net'), '', '', '', d.net, '', ''],
    }),
  },
  {
    key: 'statistics', title: () => r('statistics'), input: 'range', hint: () => r('statisticsHint'),
    table: (d: StatisticsReport) => ({
      columns: [r('businessDate'), r('rooms'), r('occupied'), r('complimentary'), r('houseUse'), r('sold'), r('arrivals'), r('departures'), r('noShows'), r('roomRevenue'), r('occShort'), 'ADR', 'RevPAR'], numeric: [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12],
      rows: d.days.map((x) => [x.business_date, `${x.rooms_total - x.rooms_out_of_order - x.rooms_house_use}`, s(x.rooms_occupied), s(x.rooms_complimentary), s(x.rooms_house_use), s(x.room_nights_sold), s(x.arrivals), s(x.departures), s(x.no_shows), x.room_revenue, x.occupancy_percent, x.adr, x.revpar]),
      footer: [r('total'), s(d.totals.available_room_nights), s(d.totals.occupied_room_nights), s(d.totals.complimentary_room_nights), s(d.totals.house_use_room_nights), s(d.totals.room_nights_sold), '', '', '', d.totals.room_revenue, d.totals.occupancy_percent, d.totals.adr, d.totals.revpar],
    }),
  },
  {
    key: 'arrivals', title: () => r('arrivalsTitle'), input: 'date', hint: () => r('arrivalsHint'),
    table: (d: ArrivalsReport) => ({
      columns: [r('reservation'), r('status'), r('guest'), r('roomType'), r('room'), r('departure'), r('party'), r('kind')], numeric: [],
      rows: d.rows.map((a) => [a.confirmation_number, a.status, a.guest, a.room_type, s(a.room), a.departure_date, `${a.adult_count}+${a.child_count}`, kind(a.occupancy_kind)]),
    }),
  },
  {
    key: 'departures', title: () => r('departuresTitle'), input: 'date', hint: () => r('departuresHint'),
    table: (d: StayListReport) => ({
      columns: [r('stay'), r('status'), r('guest'), r('room'), r('arrival'), r('departure'), r('balance'), r('kind')], numeric: [6],
      rows: d.rows.map((x) => [x.stay_number, s(x.status), x.guest, s(x.room), x.arrival_date, x.departure_date, x.balance, kind(x.occupancy_kind)]),
    }),
  },
  {
    key: 'in-house', title: () => r('inHouse'), input: 'none', hint: () => r('inHouseHint'),
    table: (d: StayListReport) => ({
      columns: [r('stay'), r('guest'), r('room'), r('arrival'), r('departure'), r('party'), r('balance'), r('kind')], numeric: [6],
      rows: d.rows.map((x) => [x.stay_number, x.guest, s(x.room), x.arrival_date, x.departure_date, `${x.adult_count}+${x.child_count}`, x.balance, kind(x.occupancy_kind)]),
    }),
  },
  {
    key: 'housekeeping-productivity', title: () => r('hkProductivity'), input: 'range', hint: () => r('hkProductivityHint'),
    table: (d: HousekeepingProductivityReport) => ({
      columns: [r('businessDate'), r('housekeeper'), r('cleaned'), r('inspected'), r('tasksDone'), r('tasksSkipped'), r('avgMinutes')], numeric: [2, 3, 4, 5, 6],
      rows: d.lines.map((l) => [l.business_date, l.user, s(l.rooms_cleaned), s(l.rooms_inspected), s(l.tasks_done), s(l.tasks_skipped), l.avg_task_minutes]),
      note: d.people.length ? r('overRange', { people: d.people.map((p) => r('person', { user: p.user, cleaned: p.rooms_cleaned, done: p.tasks_done, days: p.days_worked })).join('; ') }) : undefined,
    }),
  },
  {
    key: 'housekeeping-dirty-rooms', title: () => r('dirtyRooms'), input: 'hours', hint: () => r('dirtyRoomsHint'),
    table: (d: HousekeepingDirtyRoomsReport) => ({
      columns: [r('room'), r('floor'), r('type'), r('status'), r('sinceUtc'), r('hours'), r('occupancy'), r('priority'), 'DND', r('block')], numeric: [5],
      rows: d.rows.map((x) => [x.room_number, s(x.floor), x.room_type, x.status, x.since, s(x.hours), x.occupancy, x.priority, x.dnd ? r('yes') : '', s(x.block)]),
    }),
  },
  {
    key: 'maintenance', title: () => r('maintenance'), input: 'range', hint: () => r('maintenanceHint'),
    table: (d: MaintenanceReport) => ({
      columns: [r('category'), r('reported'), r('resolved'), r('cancelled'), r('stillOpen'), r('avgHours')], numeric: [1, 2, 3, 4, 5],
      rows: d.lines.map((l) => [l.category, s(l.reported), s(l.resolved), s(l.cancelled), s(l.still_open), l.avg_hours_to_resolve]),
      footer: [r('total'), s(d.totals.reported), s(d.totals.resolved), s(d.totals.cancelled), s(d.totals.still_open), d.totals.avg_hours_to_resolve],
      note: r('backlog', { open: d.backlog.open_now, high: d.backlog.high_priority, hours: d.backlog.oldest_hours }),
    }),
  },
]
