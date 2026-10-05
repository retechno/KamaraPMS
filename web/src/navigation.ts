import type { Component } from 'vue'
import { BedDouble, ConciergeBell, Landmark, MoonStar, Receipt, Settings } from 'lucide-vue-next'

/**
 * Application navigation. Labels are translation keys (`nav.items.<id>`, `nav.sections.<id>`, `nav.groups.<group>`),
 * so the menu follows the language. An item shows to everyone unless it is `adminOnly`; what a person may do on the
 * page is still decided by the page and the API.
 */
export interface NavItem {
  id: string
  to: string
  adminOnly?: boolean // shown to tenant administrators only
  group?: string // a sub-heading inside the section (finance)
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
    ],
  },
  {
    id: 'finance',
    icon: Landmark,
    items: [
      { id: 'chartOfAccounts', to: '/accounting/accounts', group: 'accounting' },
      { id: 'systemAccounts', to: '/accounting/mapping', group: 'accounting' },
      { id: 'journals', to: '/accounting/journals', group: 'accounting' },
      { id: 'periods', to: '/accounting/periods', group: 'accounting' },
      { id: 'fiscalYears', to: '/accounting/fiscal-years', group: 'accounting' },
      { id: 'departments', to: '/accounting/departments', group: 'accounting' },
      { id: 'trialBalance', to: '/accounting/trial-balance', group: 'accounting' },
      { id: 'generalLedger', to: '/accounting/ledger', group: 'accounting' },
      { id: 'incomeStatement', to: '/accounting/income-statement', group: 'accounting' },
      { id: 'balanceSheet', to: '/accounting/balance-sheet', group: 'accounting' },
      { id: 'cashFlow', to: '/accounting/cash-flow', group: 'accounting' },
      { id: 'controlAccounts', to: '/accounting/reconciliation', group: 'accounting' },
      { id: 'suppliers', to: '/payables/suppliers', group: 'payables' },
      { id: 'supplierBills', to: '/payables/bills', group: 'payables' },
      { id: 'supplierPayments', to: '/payables/payments', group: 'payables' },
      { id: 'payablesAging', to: '/payables/aging', group: 'payables' },
      { id: 'taxStatus', to: '/tax/status', group: 'tax' },
      { id: 'filingProfiles', to: '/tax/profiles', group: 'tax' },
      { id: 'taxReturns', to: '/tax/returns', group: 'tax' },
      { id: 'taxInvoices', to: '/tax/invoices', group: 'tax' },
      { id: 'taxOwed', to: '/tax/liability', group: 'tax' },
      { id: 'bankAccounts', to: '/bank/accounts', group: 'bank' },
      { id: 'bankStatements', to: '/bank/statements', group: 'bank' },
      { id: 'bankCards', to: '/bank/cards', group: 'bank' },
      { id: 'budgets', to: '/budget', group: 'budget' },
      { id: 'budgetVsActual', to: '/budget/vs-actual', group: 'budget' },
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
      { id: 'yieldRules', to: '/setup/yield-rules' },
      { id: 'auditTrail', to: '/audit' },
    ],
  },
]

/** The sections a person sees: items for administrators only are left out, and a section left empty disappears. */
export function visibleNavigation(isAdmin: boolean): NavSection[] {
  return navigation
    .map((s) => ({ ...s, items: s.items.filter((i) => !i.adminOnly || isAdmin) }))
    .filter((s) => s.items.length > 0)
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
