import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'
import { describe, expect, it } from 'vitest'

/**
 * A checkbox is the `Checkbox` component (components/ui/checkbox): its label is 44 px high for a finger and a box with no text is a 44 x 44 area. A raw `<input type="checkbox">`
 * is only allowed where one already was when the component came: the list below is those files and how many each holds. A count may go down (a page moves to the component, and
 * its number here goes down or its line goes away); it may never go up, and a file that is not in the list may hold none.
 */
const LEGACY: Record<string, number> = {
  'components/AutoAssignDialog.vue': 1,
  'components/StayActions.vue': 1,
  'components/banquet/BanquetCloseDialog.vue': 1,
  'components/banquet/BanquetPostDialog.vue': 1,
  'components/banquet/BanquetSessionDialog.vue': 3,
  'components/banquet/BanquetTemplateDialog.vue': 2,
  'components/folio/OutletLockForm.vue': 2,
  'components/groups/GroupBulkRooms.vue': 1,
  'components/groups/GroupCheckIn.vue': 1,
  'components/groups/GroupMasterFolio.vue': 3,
  'components/tape/TapeMoveDialog.vue': 1,
  'views/accounting/ChartOfAccountsView.vue': 4,
  'views/accounts/CityLedgerOverdueView.vue': 1,
  'views/accounts/CityLedgerView.vue': 1,
  'views/accounts/CompaniesView.vue': 3,
  'views/accounts/GroupDetailView.vue': 1,
  'views/accounts/GroupsView.vue': 1,
  'views/assets/AssetMoveDialog.vue': 2,
  'views/assets/AssetSettingsView.vue': 1,
  'views/audit/AuditTrailView.vue': 1,
  'views/bank/BankAccountsView.vue': 1,
  'views/banquet/BanquetCatalogView.vue': 2,
  'views/banquet/BanquetEventsView.vue': 1,
  'views/banquet/BanquetRoomsView.vue': 3,
  'views/banquet/BanquetSettingsView.vue': 1,
  'views/banquet/reports/BanquetForecastView.vue': 1,
  'views/billing/CashierShiftView.vue': 2,
  'views/billing/ChargeCodesView.vue': 1,
  'views/billing/FolioView.vue': 1,
  'views/billing/FoliosView.vue': 1,
  'views/billing/RateItemsSection.vue': 2,
  'views/budget/BudgetEditorView.vue': 1,
  'views/frontdesk/CheckInPanel.vue': 2,
  'views/frontdesk/DeparturesView.vue': 1,
  'views/frontdesk/WalkInView.vue': 2,
  'views/inventory/InventoryCategoryFormView.vue': 1,
  'views/inventory/InventoryItemFormView.vue': 1,
  'views/inventory/InventoryUnitFormView.vue': 1,
  'views/inventory/InventoryWarehouseFormView.vue': 1,
  'views/inventory/movements/AmenitiesPlanView.vue': 1,
  'views/inventory/movements/RequisitionView.vue': 1,
  'views/inventory/purchasing/ApprovalChainsView.vue': 4,
  'views/inventory/purchasing/PurchaseOrderView.vue': 1,
  'views/inventory/purchasing/PurchaseRequestView.vue': 2,
  'views/inventory/purchasing/ReceiptView.vue': 1,
  'views/inventory/reports/RecipeCostsView.vue': 1,
  'views/inventory/reports/TopUsageView.vue': 1,
  'views/nightaudit/NightAuditArchiveView.vue': 1,
  'views/nightaudit/NightAuditRecipientsView.vue': 2,
  'views/payables/BillsView.vue': 1,
  'views/payables/CreditNotesView.vue': 1,
  'views/payables/SuppliersView.vue': 2,
  'views/pos/PosBillView.vue': 1,
  'views/pos/PosMenuSetupView.vue': 3,
  'views/pos/PosOutletsView.vue': 1,
  'views/pos/PosProductsView.vue': 2,
  'views/pos/PosStationsView.vue': 4,
  'views/rates/RateGridView.vue': 2,
  'views/rates/RatePlansView.vue': 3,
  'views/rates/RestrictionsView.vue': 6,
  'views/rates/YieldRulesView.vue': 2,
  'views/reservations/AvailabilityCalendarView.vue': 2,
  'views/reservations/NewReservationView.vue': 3,
  'views/reservations/ReservationDetailView.vue': 1,
  'views/rooms/BedTypesView.vue': 1,
  'views/rooms/HousekeepingMyView.vue': 1,
  'views/rooms/HousekeepingView.vue': 2,
  'views/rooms/MaintenanceView.vue': 1,
  'views/rooms/RoomTypesView.vue': 1,
  'views/rooms/RoomsView.vue': 1,
  'views/setup/PropertyFormView.vue': 3,
  'views/setup/RoleFormView.vue': 1,
  'views/setup/UserFormView.vue': 2,
  'views/tax/TaxProfilesView.vue': 2,
  'views/tax/TaxStatusView.vue': 1,
}

const root = join(__dirname, '..', '..', '..') // web/src
const SKIP = (path: string): boolean => /\.(test|spec)\.[tj]sx?$/.test(path) || path.replaceAll('\\', '/').endsWith('components/ui/checkbox/Checkbox.vue')

function sources(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name)
    if (statSync(path).isDirectory()) return name === 'node_modules' ? [] : sources(path)
    return /\.(vue|[tj]sx?)$/.test(name) && !SKIP(path) ? [path] : []
  })
}

// A tag may run over several lines and hold the attributes in any order; a quoted value may hold a ">" (an arrow function, a comparison), so the quotes are read whole.
const TAG = /<input\b(?:[^>"']|"[^"]*"|'[^']*')*?(?<=\s)type\s*=\s*(?:"checkbox"|'checkbox'|checkbox(?![\w-]))/g
const RENDERED = /\btype\s*:\s*['"]checkbox['"]/g // h('input', { type: 'checkbox' })

/** How many raw checkboxes a source holds. */
function rawCheckboxes(text: string): number {
  return (text.match(TAG)?.length ?? 0) + (text.match(RENDERED)?.length ?? 0)
}

const FIX = 'Use the Checkbox component (import { Checkbox } from "@/components/ui/checkbox"): it is 44 px for a finger and holds the label. An entry of LEGACY may only be removed or lowered, never added or raised.'

describe('raw checkboxes', () => {
  it('reads a tag whatever the order of its attributes, over several lines, with a ">" in a value', () => {
    expect(rawCheckboxes('<input type="checkbox" />')).toBe(1)
    expect(rawCheckboxes('<input v-model="a" class="x" type="checkbox" :value="1">')).toBe(1)
    expect(rawCheckboxes('<input\n  :checked="a > b"\n  :disabled="x => y"\n  type=\'checkbox\'\n  @change="go"\n/>')).toBe(1)
    expect(rawCheckboxes("h('input', { type: 'checkbox' })")).toBe(1)
    expect(rawCheckboxes('<input type="radio" /><input type="text" data-type="checkbox" /><Checkbox type="checkbox" />')).toBe(0)
    expect(rawCheckboxes('<input type="checkbox" /><label><input class="a" type="checkbox" /></label>')).toBe(2)
  })

  it('are only where they already were, and are not more', () => {
    const found: Record<string, number> = {}
    for (const file of sources(root)) {
      const n = rawCheckboxes(readFileSync(file, 'utf8'))
      if (n > 0) found[relative(root, file).replaceAll('\\', '/')] = n
    }
    const grown = Object.entries(found).filter(([file, n]) => n > (LEGACY[file] ?? 0))
    const message = grown.map(([file, n]) => `  ${file}: ${n} raw checkbox${n === 1 ? '' : 'es'}, ${LEGACY[file] ?? 0} allowed`).join('\n')
    expect(grown, `Raw <input type="checkbox"> where it is not allowed:\n${message}\n${FIX}`).toEqual([])
    // the list is not kept longer than it is true: a count that went down, or a file that is clean now, is taken out of it (lowered), so it cannot grow back
    const stale = Object.entries(LEGACY).filter(([file, n]) => (found[file] ?? 0) < n)
    expect(stale.map(([file, n]) => `${file}: ${n} listed, ${found[file] ?? 0} found`), `LEGACY lists more than there is: lower the count or remove the entry.`).toEqual([])
  }, 60000)
})
