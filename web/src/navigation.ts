import type { Component } from 'vue'
import { BedDouble, BookOpen, ConciergeBell, Landmark, MoonStar, PiggyBank, Percent, Receipt, Settings, Truck } from 'lucide-vue-next'

/**
 * Application navigation. Labels are translation keys (`nav.items.<id>`, `nav.sections.<id>`), so the menu follows the
 * language. An item shows to everyone unless it is `adminOnly` or names a `permission` the person does not have at the
 * property; what a person may do on the page is still decided by the page and the API.
 */
export interface NavItem {
  id: string
  to: string
  adminOnly?: boolean // shown to tenant administrators only
  permission?: string // shown only to who has this permission at the open property (for pages that are for some roles: the manager's performance page)
}

export interface NavSection {
  id: string
  icon: Component
  items: NavItem[]
  /** Open when the person has not chosen otherwise; the section of the current page is always open. */
  defaultOpen?: boolean
}

export const navigation: NavSection[] = [
  {
    id: 'frontDesk',
    icon: ConciergeBell,
    defaultOpen: true,
    items: [
      { id: 'dashboard', to: '/' },
      { id: 'reservations', to: '/reservations' },
      { id: 'tapeChart', to: '/reservations/tape' },
      { id: 'availability', to: '/availability' },
      { id: 'arrivals', to: '/arrivals' },
      { id: 'inHouse', to: '/in-house' },
      { id: 'departures', to: '/departures' },
      { id: 'walkIn', to: '/walk-in' },
      { id: 'guests', to: '/guests' },
      { id: 'groups', to: '/groups' },
    ],
  },
  {
    id: 'rooms',
    icon: BedDouble,
    defaultOpen: true,
    items: [
      { id: 'roomStatus', to: '/room-status' },
      { id: 'housekeeping', to: '/housekeeping' },
      { id: 'cleaningList', to: '/housekeeping/tasks' },
      { id: 'maintenance', to: '/maintenance' },
      { id: 'lostFound', to: '/lost-found' },
      { id: 'roomBlocks', to: '/room-blocks' },
    ],
  },
  {
    id: 'billing',
    icon: Receipt,
    defaultOpen: true,
    items: [
      { id: 'folios', to: '/folios' },
      { id: 'cashier', to: '/cashier' },
      { id: 'cashierShifts', to: '/cashier/shifts' },
      { id: 'roomCharges', to: '/room-charges' },
      { id: 'cityLedger', to: '/city-ledger' },
      { id: 'cityLedgerOverdue', to: '/city-ledger/overdue' },
    ],
  },
  {
    id: 'endOfDay',
    icon: MoonStar,
    defaultOpen: true,
    items: [
      { id: 'nightAudit', to: '/night-audit' },
      { id: 'reports', to: '/reports' },
      { id: 'performance', to: '/performance', permission: 'report.view' },
    ],
  },
  {
    id: 'accounting',
    icon: BookOpen,
    items: [
      { id: 'chartOfAccounts', to: '/accounting/accounts' },
      { id: 'systemAccounts', to: '/accounting/mapping' },
      { id: 'journals', to: '/accounting/journals' },
      { id: 'periods', to: '/accounting/periods' },
      { id: 'fiscalYears', to: '/accounting/fiscal-years' },
      { id: 'departments', to: '/accounting/departments' },
      { id: 'trialBalance', to: '/accounting/trial-balance' },
      { id: 'generalLedger', to: '/accounting/ledger' },
      { id: 'incomeStatement', to: '/accounting/income-statement' },
      { id: 'departmentReport', to: '/accounting/department-report' },
      { id: 'balanceSheet', to: '/accounting/balance-sheet' },
      { id: 'cashFlow', to: '/accounting/cash-flow' },
      { id: 'controlAccounts', to: '/accounting/reconciliation' },
    ],
  },
  {
    id: 'payables',
    icon: Truck,
    items: [
      { id: 'suppliers', to: '/payables/suppliers' },
      { id: 'supplierBills', to: '/payables/bills' },
      { id: 'supplierCredits', to: '/payables/credit-notes' },
      { id: 'supplierPayments', to: '/payables/payments' },
      { id: 'payablesAging', to: '/payables/aging' },
    ],
  },
  {
    id: 'tax',
    icon: Percent,
    items: [
      { id: 'taxStatus', to: '/tax/status' },
      { id: 'filingProfiles', to: '/tax/profiles' },
      { id: 'taxReturns', to: '/tax/returns' },
      { id: 'taxInvoices', to: '/tax/invoices' },
      { id: 'taxOwed', to: '/tax/liability' },
    ],
  },
  {
    id: 'bank',
    icon: Landmark,
    items: [
      { id: 'bankAccounts', to: '/bank/accounts' },
      { id: 'bankStatements', to: '/bank/statements' },
      { id: 'bankCards', to: '/bank/cards' },
    ],
  },
  {
    id: 'budget',
    icon: PiggyBank,
    items: [
      { id: 'budgets', to: '/budget' },
      { id: 'budgetVsActual', to: '/budget/vs-actual' },
    ],
  },
  {
    id: 'setup',
    icon: Settings,
    items: [
      { id: 'properties', to: '/setup/properties', adminOnly: true },
      { id: 'users', to: '/setup/users', adminOnly: true },
      { id: 'roles', to: '/setup/roles', adminOnly: true },
      { id: 'roomTypes', to: '/setup/room-types' },
      { id: 'bedTypes', to: '/setup/bed-types' },
      { id: 'roomsSetup', to: '/setup/rooms' },
      { id: 'taxesService', to: '/setup/taxes' },
      { id: 'chargeCodes', to: '/setup/charge-codes' },
      { id: 'companies', to: '/setup/companies' },
      { id: 'ratePlans', to: '/setup/rate-plans' },
      { id: 'freeQuotas', to: '/setup/free-quotas' },
      { id: 'rateGrid', to: '/setup/rates' },
      { id: 'bedSupplements', to: '/setup/bed-supplements' },
      { id: 'restrictions', to: '/setup/restrictions' },
      { id: 'yieldRules', to: '/setup/yield-rules' },
      { id: 'auditTrail', to: '/audit' },
    ],
  },
]

/**
 * The sections a person sees: items for administrators only are left out, an item that names a permission is left out unless `can` says the person has it, and a section left empty disappears.
 */
export function visibleNavigation(isAdmin: boolean, can?: (permission: string) => boolean, menu: NavSection[] = navigation): NavSection[] {
  const allowed = (i: NavItem): boolean => (!i.adminOnly || isAdmin) && (!i.permission || (can ? can(i.permission) : false))
  return menu.map((s) => ({ ...s, items: s.items.filter(allowed) })).filter((s) => s.items.length > 0)
}

/** An item of the navigation by its id, with its section; undefined for an id that is not (or no longer) in the menu. */
export function findNavItem(id: string, sections: NavSection[] = navigation): ActiveNav | undefined {
  for (const section of sections) {
    const item = section.items.find((i) => i.id === id)
    if (item) return { section, item }
  }
  return undefined
}

export interface ActiveNav {
  section: NavSection
  item: NavItem
}

/**
 * The menu item a path belongs to: the one with the longest matching route, so `/reservations/tape` is the tape chart
 * and `/reservations/12` a reservation. `/` matches only itself.
 */
export function activeNav(path: string, sections: NavSection[] = navigation): ActiveNav | null {
  let best: ActiveNav | null = null
  for (const section of sections) {
    for (const item of section.items) {
      const hit = item.to === '/' ? path === '/' : path === item.to || path.startsWith(`${item.to}/`)
      if (hit && (!best || item.to.length > best.item.to.length)) best = { section, item }
    }
  }
  return best
}
