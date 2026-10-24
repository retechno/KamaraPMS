import type { Component } from 'vue'
import { BedDouble, BookOpen, Boxes, CalendarRange, ConciergeBell, Landmark, MoonStar, PiggyBank, Percent, Receipt, Settings, Store, Truck } from 'lucide-vue-next'

/**
 * Application navigation. Labels are translation keys (`nav.items.<id>`, `nav.sections.<id>`), so the menu follows the
 * language. An item shows to everyone unless it is `adminOnly` or names a `permission` the person does not have at the
 * property; what a person may do on the page is still decided by the page and the API.
 */
export interface NavItem {
  id: string
  to: string
  adminOnly?: boolean // shown to tenant administrators only
  /**
   * Shown only to who has this permission at the open property: the one that the read endpoint of the page asks for (not the one to change things). A list means any of them.
   * No permission: the endpoint lets in every member of the property.
   */
  permission?: string | string[]
  /** The optional module of the plan the page belongs to (docs/architecture/26-subscriptions.md): a tenant without it does not see the item. */
  module?: string
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
      { id: 'reservations', to: '/reservations', permission: 'reservation.read' },
      { id: 'tapeChart', to: '/reservations/tape', permission: 'reservation.read' },
      { id: 'availability', to: '/availability', permission: 'reservation.read' },
      { id: 'arrivals', to: '/arrivals', permission: 'reservation.read' },
      { id: 'inHouse', to: '/in-house', permission: 'reservation.read' },
      { id: 'departures', to: '/departures', permission: 'reservation.read' },
      { id: 'walkIn', to: '/walk-in', permission: 'reservation.read' },
      { id: 'guests', to: '/guests', permission: 'guest.read' },
      { id: 'groups', to: '/groups', permission: 'reservation.read' },
    ],
  },
  {
    id: 'rooms',
    icon: BedDouble,
    defaultOpen: true,
    items: [
      { id: 'roomStatus', to: '/room-status' },
      { id: 'housekeeping', to: '/housekeeping' },
      { id: 'myTasks', to: '/housekeeping/my', permission: 'housekeeping.update' },
      { id: 'cleaningList', to: '/housekeeping/tasks' },
      { id: 'maintenance', to: '/maintenance', permission: 'maintenance.report' },
      { id: 'lostFound', to: '/lost-found', permission: 'lostfound.report' },
      { id: 'roomBlocks', to: '/room-blocks' },
    ],
  },
  {
    id: 'billing',
    icon: Receipt,
    defaultOpen: true,
    items: [
      { id: 'folios', to: '/folios', permission: 'folio.read' },
      { id: 'cashier', to: '/cashier', permission: 'cashier.shift' },
      { id: 'cashierShifts', to: '/cashier/shifts', permission: ['cashier.shift', 'cashier.shift_manage'] },
      { id: 'roomCharges', to: '/room-charges', permission: ['nightaudit.run', 'folio.post_charge'] },
      { id: 'cityLedger', to: '/city-ledger', permission: 'cityledger.read' },
      { id: 'cityLedgerOverdue', to: '/city-ledger/overdue', permission: 'cityledger.read' },
      { id: 'outletCharges', to: '/outlet-charges', permission: ['folio.outlet_charge_open', 'folio.read'], module: 'POS' },
    ],
  },
  {
    id: 'endOfDay',
    icon: MoonStar,
    defaultOpen: true,
    items: [
      { id: 'nightAudit', to: '/night-audit', permission: 'nightaudit.run' },
      { id: 'reports', to: '/reports', permission: 'report.view' },
      { id: 'performance', to: '/performance', permission: 'report.view' },
    ],
  },
  {
    id: 'pos',
    icon: Store,
    items: [
      { id: 'posFloor', to: '/pos/floor', permission: ['pos.order', 'pos.cashier'], module: 'POS' },
      { id: 'posWaiter', to: '/pos/waiter', permission: 'pos.order', module: 'POS' },
      { id: 'posKds', to: '/pos/kds', permission: 'pos.kds', module: 'POS' },
      { id: 'posReport', to: '/pos/report', permission: 'pos.report', module: 'POS' },
      { id: 'posOutlets', to: '/pos/outlets', permission: 'pos.manage', module: 'POS' },
      { id: 'posProducts', to: '/pos/products', permission: 'pos.manage', module: 'POS' },
      { id: 'posMenuSetup', to: '/pos/menu', permission: 'pos.manage', module: 'POS' },
      { id: 'posFloorPlan', to: '/pos/floor-plan', permission: 'pos.manage', module: 'POS' },
      { id: 'posStations', to: '/pos/stations', permission: 'pos.manage', module: 'POS' },
    ],
  },
  {
    id: 'inventory',
    icon: Boxes,
    items: [
      { id: 'inventoryHome', to: '/inventory', permission: ['inventory.view', 'inventory.manage', 'inventory.opening'], module: 'INVENTORY' },
      { id: 'inventoryItems', to: '/inventory/items', permission: ['inventory.view', 'inventory.manage', 'inventory.opening'], module: 'INVENTORY' },
      { id: 'inventoryOpenings', to: '/inventory/openings', permission: ['inventory.view', 'inventory.manage', 'inventory.opening'], module: 'INVENTORY' },
      { id: 'inventoryWarehouses', to: '/inventory/warehouses', permission: ['inventory.view', 'inventory.manage', 'inventory.opening'], module: 'INVENTORY' },
      { id: 'inventoryCategories', to: '/inventory/categories', permission: ['inventory.view', 'inventory.manage', 'inventory.opening'], module: 'INVENTORY' },
      { id: 'inventoryUnits', to: '/inventory/units', permission: ['inventory.view', 'inventory.manage', 'inventory.opening'], module: 'INVENTORY' },
      { id: 'inventoryCosting', to: '/inventory/costing', permission: ['inventory.view', 'inventory.manage', 'inventory.opening'], module: 'INVENTORY' },
      { id: 'inventoryPurchasing', to: '/inventory/purchasing', permission: ['inventory.view', 'inventory.request', 'inventory.purchase', 'inventory.receive', 'inventory.bill', 'inventory.approve', 'inventory.cost_control', 'inventory.final_approve'], module: 'INVENTORY' },
      { id: 'inventoryDrafts', to: '/inventory/bill-drafts', permission: ['inventory.bill', 'inventory.view'], module: 'INVENTORY' },
      { id: 'inventoryMovements', to: '/inventory/requisitions', permission: ['inventory.view', 'inventory.request', 'inventory.purchase', 'inventory.receive', 'inventory.move', 'inventory.count', 'inventory.approve', 'inventory.dept_approve', 'inventory.cost_control', 'inventory.final_approve', 'inventory.sr_final_approve'], module: 'INVENTORY' },
      { id: 'inventoryIssues', to: '/inventory/issues', permission: ['inventory.view', 'inventory.request', 'inventory.purchase', 'inventory.receive', 'inventory.move', 'inventory.count', 'inventory.approve', 'inventory.dept_approve', 'inventory.cost_control', 'inventory.final_approve', 'inventory.sr_final_approve'], module: 'INVENTORY' },
      { id: 'inventoryCounts', to: '/inventory/counts', permission: ['inventory.view', 'inventory.request', 'inventory.purchase', 'inventory.receive', 'inventory.move', 'inventory.count', 'inventory.approve', 'inventory.dept_approve', 'inventory.cost_control', 'inventory.final_approve', 'inventory.sr_final_approve'], module: 'INVENTORY' },
      { id: 'inventoryAmenities', to: '/inventory/amenities', permission: ['inventory.view', 'inventory.manage'], module: 'INVENTORY' },
      { id: 'inventoryPosGap', to: '/inventory/pos-gap', permission: ['inventory.view', 'inventory.manage'], module: 'INVENTORY' },
      { id: 'inventoryApprovals', to: '/inventory/approvals', permission: ['inventory.approve', 'inventory.dept_approve', 'inventory.cost_control', 'inventory.final_approve', 'inventory.sr_final_approve'], module: 'INVENTORY' },
      { id: 'inventoryChains', to: '/inventory/approval-chains', permission: 'inventory.manage', module: 'INVENTORY' },
    ],
  },
  {
    id: 'banquet',
    icon: CalendarRange,
    items: [
      { id: 'banquetCalendar', to: '/banquet', permission: ['banquet.manage', 'banquet.confirm', 'banquet.read'], module: 'BANQUET' },
      { id: 'banquetEvents', to: '/banquet/events', permission: ['banquet.manage', 'banquet.confirm', 'banquet.read'], module: 'BANQUET' },
      { id: 'banquetRooms', to: '/banquet/rooms', permission: ['banquet.setup', 'banquet.manage', 'banquet.confirm', 'banquet.read'], module: 'BANQUET' },
      { id: 'banquetCatalog', to: '/banquet/catalog', permission: ['banquet.setup', 'banquet.manage', 'banquet.confirm', 'banquet.read'], module: 'BANQUET' },
      { id: 'banquetSettings', to: '/banquet/settings', permission: 'banquet.setup', module: 'BANQUET' },
    ],
  },
  {
    id: 'accounting',
    icon: BookOpen,
    items: [
      { id: 'chartOfAccounts', to: '/accounting/accounts', permission: 'accounting.view' },
      { id: 'systemAccounts', to: '/accounting/mapping', permission: 'accounting.view' },
      { id: 'journals', to: '/accounting/journals', permission: 'accounting.view' },
      { id: 'periods', to: '/accounting/periods', permission: 'accounting.view' },
      { id: 'fiscalYears', to: '/accounting/fiscal-years', permission: 'accounting.view' },
      { id: 'departments', to: '/accounting/departments', permission: 'accounting.view' },
      { id: 'trialBalance', to: '/accounting/trial-balance', permission: 'accounting.view' },
      { id: 'generalLedger', to: '/accounting/ledger', permission: 'accounting.view' },
      { id: 'incomeStatement', to: '/accounting/income-statement', permission: 'accounting.view' },
      { id: 'departmentReport', to: '/accounting/department-report', permission: 'accounting.view' },
      { id: 'balanceSheet', to: '/accounting/balance-sheet', permission: 'accounting.view' },
      { id: 'cashFlow', to: '/accounting/cash-flow', permission: 'accounting.view' },
      { id: 'controlAccounts', to: '/accounting/reconciliation', permission: 'accounting.view' },
    ],
  },
  {
    id: 'payables',
    icon: Truck,
    items: [
      { id: 'suppliers', to: '/payables/suppliers', permission: 'payables.view' },
      { id: 'supplierBills', to: '/payables/bills', permission: 'payables.view' },
      { id: 'supplierCredits', to: '/payables/credit-notes', permission: 'payables.view' },
      { id: 'supplierPayments', to: '/payables/payments', permission: 'payables.view' },
      { id: 'payablesAging', to: '/payables/aging', permission: 'payables.view' },
    ],
  },
  {
    id: 'tax',
    icon: Percent,
    items: [
      { id: 'taxStatus', to: '/tax/status', permission: 'tax.view' },
      { id: 'filingProfiles', to: '/tax/profiles', permission: 'tax.view' },
      { id: 'taxReturns', to: '/tax/returns', permission: 'tax.view' },
      { id: 'taxInvoices', to: '/tax/invoices', permission: 'tax.view' },
      { id: 'taxOwed', to: '/tax/liability', permission: 'tax.view' },
    ],
  },
  {
    id: 'bank',
    icon: Landmark,
    items: [
      { id: 'bankAccounts', to: '/bank/accounts', permission: 'bank.view' },
      { id: 'bankStatements', to: '/bank/statements', permission: 'bank.view' },
      { id: 'bankCards', to: '/bank/cards', permission: 'bank.view' },
    ],
  },
  {
    id: 'budget',
    icon: PiggyBank,
    items: [
      { id: 'budgets', to: '/budget', permission: 'budget.view' },
      { id: 'budgetVsActual', to: '/budget/vs-actual', permission: 'budget.view' },
    ],
  },
  {
    id: 'setup',
    icon: Settings,
    items: [
      { id: 'properties', to: '/setup/properties', adminOnly: true },
      { id: 'users', to: '/setup/users', adminOnly: true },
      { id: 'roles', to: '/setup/roles', adminOnly: true },
      { id: 'roomTypes', to: '/setup/room-types', permission: 'room.manage' },
      { id: 'bedTypes', to: '/setup/bed-types', permission: 'room.manage' },
      { id: 'roomsSetup', to: '/setup/rooms', permission: 'room.manage' },
      { id: 'taxesService', to: '/setup/taxes', permission: 'billing_config.manage' },
      { id: 'chargeCodes', to: '/setup/charge-codes', permission: 'billing_config.manage' },
      { id: 'companies', to: '/setup/companies', permission: 'company.manage' },
      { id: 'ratePlans', to: '/setup/rate-plans', permission: 'rate.manage' },
      { id: 'freeQuotas', to: '/setup/free-quotas', permission: 'rate.manage' },
      { id: 'approvalRules', to: '/setup/approval-rules', permission: 'property.manage' },
      { id: 'rateGrid', to: '/setup/rates', permission: 'rate.manage' },
      { id: 'bedSupplements', to: '/setup/bed-supplements', permission: 'rate.manage' },
      { id: 'restrictions', to: '/setup/restrictions', permission: 'rate.manage' },
      { id: 'yieldRules', to: '/setup/yield-rules', permission: 'rate.manage' },
      { id: 'auditTrail', to: '/audit', permission: 'audit.read' },
    ],
  },
]

/** Does `can` allow the item: the permission it names, or any of the permissions when it names a list. */
export const permitted = (i: Pick<NavItem, 'permission'>, can: (permission: string) => boolean): boolean => !i.permission || [i.permission].flat().some((p) => can(p))

/**
 * The sections a person sees: items for administrators only are left out, an item that names a permission is left out unless `can` says the person has it, and a section left empty disappears.
 */
export function visibleNavigation(isAdmin: boolean, can?: (permission: string) => boolean, menu: NavSection[] = navigation, hasModule?: (module: string) => boolean): NavSection[] {
  const allowed = (i: NavItem): boolean =>
    (!i.adminOnly || isAdmin) && (!i.permission || (can ? permitted(i, can) : false)) && (!i.module || !hasModule || hasModule(i.module))
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
