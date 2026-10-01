/**
 * Application navigation. Items become enabled as their milestone lands
 * (docs/architecture/07-milestones.md); until then they are shown disabled so
 * the shape of the product is visible from day one.
 */
export interface NavItem {
  label: string
  to?: string // route path when available
  milestone: string
  adminOnly?: boolean // shown to tenant administrators only
}

export interface NavSection {
  title: string
  items: NavItem[]
}

export const navigation: NavSection[] = [
  {
    title: 'Front office',
    items: [
      { label: 'Dashboard', to: '/', milestone: 'M0' },
      { label: 'Guests', to: '/guests', milestone: 'M4' },
      { label: 'Reservations', to: '/reservations', milestone: 'M8' },
      { label: 'Groups', to: '/groups', milestone: 'S1' },
      { label: 'Tape chart', to: '/reservations/tape', milestone: 'M8' },
      { label: 'Arrivals', to: '/arrivals', milestone: 'M10' },
      { label: 'Walk-in', to: '/walk-in', milestone: 'M10' },
      { label: 'In-house', to: '/in-house', milestone: 'M10' },
      { label: 'Departures', to: '/departures', milestone: 'M12' },
    ],
  },
  {
    title: 'Rooms',
    items: [
      { label: 'Room status', to: '/room-status', milestone: 'M10' },
      { label: 'Housekeeping', to: '/housekeeping', milestone: 'M3' },
      { label: 'Cleaning list', to: '/housekeeping/tasks', milestone: 'S2' },
      { label: 'Maintenance', to: '/maintenance', milestone: 'S2' },
      { label: 'Lost & found', to: '/lost-found', milestone: 'S2' },
      { label: 'Room blocks', to: '/room-blocks', milestone: 'M3' },
    ],
  },
  {
    title: 'Billing',
    items: [
      { label: 'Folios', to: '/folios', milestone: 'M9' },
      { label: 'Cashier', to: '/cashier', milestone: 'M9' },
      { label: 'Room charges', to: '/room-charges', milestone: 'M11' },
      { label: 'City ledger', to: '/city-ledger', milestone: 'S1' },
    ],
  },
  {
    title: 'Accounting',
    items: [
      { label: 'Chart of accounts', to: '/accounting/accounts', milestone: 'S3' },
      { label: 'System accounts', to: '/accounting/mapping', milestone: 'S3' },
      { label: 'Journals', to: '/accounting/journals', milestone: 'S3' },
      { label: 'Periods', to: '/accounting/periods', milestone: 'S3' },
      { label: 'Fiscal years', to: '/accounting/fiscal-years', milestone: 'S3' },
      { label: 'Trial balance', to: '/accounting/trial-balance', milestone: 'S3' },
      { label: 'General ledger', to: '/accounting/ledger', milestone: 'S3' },
      { label: 'Income statement', to: '/accounting/income-statement', milestone: 'S3' },
      { label: 'Balance sheet', to: '/accounting/balance-sheet', milestone: 'S3' },
      { label: 'Control accounts', to: '/accounting/reconciliation', milestone: 'S3' },
    ],
  },
  {
    title: 'End of day',
    items: [
      { label: 'Night audit', to: '/night-audit', milestone: 'M13' },
      { label: 'Reports', to: '/reports', milestone: 'M14' },
    ],
  },
  {
    title: 'Setup',
    items: [
      { label: 'Properties', to: '/setup/properties', milestone: 'M1', adminOnly: true },
      { label: 'Users', to: '/setup/users', milestone: 'M2', adminOnly: true },
      { label: 'Roles', to: '/setup/roles', milestone: 'M2', adminOnly: true },
      { label: 'Room types', to: '/setup/room-types', milestone: 'M3' },
      { label: 'Rooms', to: '/setup/rooms', milestone: 'M3' },
      { label: 'Taxes & service charges', to: '/setup/taxes', milestone: 'M5' },
      { label: 'Charge codes', to: '/setup/charge-codes', milestone: 'M5' },
      { label: 'Companies', to: '/setup/companies', milestone: 'S1' },
      { label: 'Rate plans', to: '/setup/rate-plans', milestone: 'M7' },
      { label: 'Rate grid', to: '/setup/rates', milestone: 'M7' },
      { label: 'Audit trail', to: '/audit', milestone: 'M15' },
    ],
  },
]
