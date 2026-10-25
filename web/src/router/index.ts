import { createRouter, createWebHistory } from 'vue-router'
import { watch } from 'vue'
import { i18n } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { useSubscriptionStore } from '@/stores/subscription'
import { usePropertyStore } from '@/stores/property'
import { routeTitle } from '@/utils/routeTitle'
import HomeView from '@/views/HomeView.vue'

declare module 'vue-router' {
  interface RouteMeta {
    title?: string
    public?: boolean // reachable without signing in
    adminOnly?: boolean // tenant administrators only
    module?: string // an optional module of the plan the page belongs to (POS): a tenant without it goes home
    permission?: string | string[] // a permission of the open property that the page needs (the one that manages what it shows); the pages of setup name it
  }
}

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', name: 'login', component: () => import('@/views/LoginView.vue'), meta: { title: 'Sign in', public: true } },
    { path: '/', name: 'home', component: HomeView, meta: { title: 'Today' } },
    {
      path: '/housekeeping',
      name: 'housekeeping',
      component: () => import('@/views/rooms/HousekeepingView.vue'),
      meta: { title: 'Housekeeping' },
    },
    {
      path: '/housekeeping/my',
      name: 'housekeeping-my',
      component: () => import('@/views/rooms/HousekeepingMyView.vue'),
      meta: { title: 'My tasks' },
    },
    {
      path: '/housekeeping/tasks',
      name: 'housekeeping-tasks',
      component: () => import('@/views/rooms/HousekeepingTasksView.vue'),
      meta: { title: 'Cleaning list' },
    },
    {
      path: '/maintenance',
      name: 'maintenance',
      component: () => import('@/views/rooms/MaintenanceView.vue'),
      meta: { title: 'Maintenance' },
    },
    {
      path: '/lost-found',
      name: 'lost-found',
      component: () => import('@/views/rooms/LostFoundView.vue'),
      meta: { title: 'Lost and found' },
    },
    { path: '/pos/floor', name: 'pos-floor', component: () => import('@/views/pos/PosFloorView.vue'), meta: { title: 'Floor plan and bills', permission: ['pos.order', 'pos.cashier'], module: 'POS' } },
    { path: '/pos/bills/:id', name: 'pos-bill', component: () => import('@/views/pos/PosBillView.vue'), meta: { title: 'Bill', permission: ['pos.order', 'pos.cashier'], module: 'POS' } },
    { path: '/pos/waiter', name: 'pos-waiter', component: () => import('@/views/pos/PosWaiterFloorView.vue'), meta: { title: 'Waiter', permission: 'pos.order', module: 'POS' } },
    { path: '/pos/waiter/bills/:id', name: 'pos-waiter-bill', component: () => import('@/views/pos/PosWaiterOrderView.vue'), meta: { title: 'Order', permission: 'pos.order', module: 'POS' } },
    { path: '/pos/kds', name: 'pos-kds', component: () => import('@/views/pos/PosKdsView.vue'), meta: { title: 'Kitchen display', permission: 'pos.kds', module: 'POS' } },
    { path: '/pos/report', name: 'pos-report', component: () => import('@/views/pos/PosReportView.vue'), meta: { title: 'Outlet report', permission: 'pos.report', module: 'POS' } },
    { path: '/pos/outlets', name: 'pos-outlets', component: () => import('@/views/pos/PosOutletsView.vue'), meta: { title: 'Outlets', permission: 'pos.manage', module: 'POS' } },
    { path: '/pos/products', name: 'pos-products', component: () => import('@/views/pos/PosProductsView.vue'), meta: { title: 'Products and menu', permission: 'pos.manage', module: 'POS' } },
    { path: '/pos/menu', name: 'pos-menu', component: () => import('@/views/pos/PosMenuSetupView.vue'), meta: { title: 'Categories, prices and profiles', permission: 'pos.manage', module: 'POS' } },
    { path: '/pos/floor-plan', name: 'pos-floor-plan', component: () => import('@/views/pos/PosFloorPlanView.vue'), meta: { title: 'Floor plan', permission: 'pos.manage', module: 'POS' } },
    { path: '/pos/stations', name: 'pos-stations', component: () => import('@/views/pos/PosStationsView.vue'), meta: { title: 'Stations and printers', permission: 'pos.manage', module: 'POS' } },
    { path: '/banquet', name: 'banquet-calendar', component: () => import('@/views/banquet/BanquetCalendarView.vue'), meta: { title: 'Banquet calendar', permission: ['banquet.manage', 'banquet.confirm', 'banquet.read'], module: 'BANQUET' } },
    { path: '/banquet/today', name: 'banquet-today', component: () => import('@/views/banquet/BanquetTodayView.vue'), meta: { title: 'Events today', permission: ['banquet.manage', 'banquet.confirm', 'banquet.read'], module: 'BANQUET' } },
    { path: '/banquet/unposted', name: 'banquet-unposted', component: () => import('@/views/banquet/BanquetUnpostedView.vue'), meta: { title: 'Sessions not posted', permission: ['banquet.manage', 'banquet.confirm'], module: 'BANQUET' } },
    { path: '/banquet/events', name: 'banquet-events', component: () => import('@/views/banquet/BanquetEventsView.vue'), meta: { title: 'Banquet events', permission: ['banquet.manage', 'banquet.confirm', 'banquet.read'], module: 'BANQUET' } },
    { path: '/banquet/events/:id', name: 'banquet-event', component: () => import('@/views/banquet/BanquetEventView.vue'), meta: { title: 'Banquet event', permission: ['banquet.manage', 'banquet.confirm', 'banquet.read'], module: 'BANQUET' } },
    { path: '/banquet/rooms', name: 'banquet-rooms', component: () => import('@/views/banquet/BanquetRoomsView.vue'), meta: { title: 'Meeting rooms', permission: ['banquet.setup', 'banquet.manage', 'banquet.confirm', 'banquet.read'], module: 'BANQUET' } },
    { path: '/banquet/catalog', name: 'banquet-catalog', component: () => import('@/views/banquet/BanquetCatalogView.vue'), meta: { title: 'Items and packages', permission: ['banquet.setup', 'banquet.manage', 'banquet.confirm', 'banquet.read'], module: 'BANQUET' } },
    { path: '/banquet/settings', name: 'banquet-settings', component: () => import('@/views/banquet/BanquetSettingsView.vue'), meta: { title: 'Banquet settings', permission: 'banquet.setup', module: 'BANQUET' } },
    { path: '/inventory', name: 'inventory-home', component: () => import('@/views/inventory/InventoryHomeView.vue'), meta: { title: 'Inventory', permission: ['inventory.view', 'inventory.manage', 'inventory.opening'], module: 'INVENTORY' } },
    { path: '/inventory/reports/daily-cost', name: 'inventory-report-daily-cost', component: () => import('@/views/inventory/reports/DailyCostView.vue'), meta: { title: 'Daily cost', permission: ['inventory.view', 'inventory.manage', 'inventory.opening'], module: 'INVENTORY' } },
    { path: '/inventory/reports/outlet-cost', name: 'inventory-report-outlet-cost', component: () => import('@/views/inventory/reports/OutletCostView.vue'), meta: { title: 'Food and beverage cost per outlet', permission: ['inventory.view', 'inventory.manage', 'inventory.opening'], module: 'INVENTORY' } },
    { path: '/inventory/reports/fb-reconciliation', name: 'inventory-report-fb-reconciliation', component: () => import('@/views/inventory/reports/ReconciliationView.vue'), meta: { title: 'Food and beverage cost reconciliation', permission: ['inventory.view', 'inventory.manage', 'inventory.opening'], module: 'INVENTORY' } },
    { path: '/inventory/reports/stock-value', name: 'inventory-report-stock-value', component: () => import('@/views/inventory/reports/StockValueView.vue'), meta: { title: 'Value of the stock', permission: ['inventory.view', 'inventory.manage', 'inventory.opening'], module: 'INVENTORY' } },
    { path: '/inventory/reports/low-stock', name: 'inventory-report-low-stock', component: () => import('@/views/inventory/reports/LowStockView.vue'), meta: { title: 'Items below minimum', permission: ['inventory.view', 'inventory.manage', 'inventory.opening'], module: 'INVENTORY' } },
    { path: '/inventory/reports/negative-stock', name: 'inventory-report-negative-stock', component: () => import('@/views/inventory/reports/NegativeStockView.vue'), meta: { title: 'Stock below zero', permission: ['inventory.view', 'inventory.manage', 'inventory.opening'], module: 'INVENTORY' } },
    { path: '/inventory/reports/received-not-invoiced', name: 'inventory-report-received-not-invoiced', component: () => import('@/views/inventory/reports/GrniView.vue'), meta: { title: 'Received, not invoiced', permission: ['inventory.view', 'inventory.manage', 'inventory.opening'], module: 'INVENTORY' } },
    { path: '/inventory/reports/usage', name: 'inventory-report-usage', component: () => import('@/views/inventory/reports/UsageView.vue'), meta: { title: 'Usage by department', permission: ['inventory.view', 'inventory.manage', 'inventory.opening'], module: 'INVENTORY' } },
    { path: '/inventory/reports', name: 'inventory-reports', component: () => import('@/views/inventory/ReportsIndexView.vue'), meta: { title: 'Inventory reports', permission: ['inventory.view', 'inventory.manage', 'inventory.opening'], module: 'INVENTORY' } },
    { path: '/inventory/reports/waste', name: 'inventory-report-waste', component: () => import('@/views/inventory/reports/WasteView.vue'), meta: { title: 'Waste by reason', permission: ['inventory.view', 'inventory.manage', 'inventory.opening'], module: 'INVENTORY' } },
    { path: '/inventory/reports/count-variances', name: 'inventory-report-count-variances', component: () => import('@/views/inventory/reports/CountVariancesView.vue'), meta: { title: 'Count variances', permission: ['inventory.view', 'inventory.manage', 'inventory.opening'], module: 'INVENTORY' } },
    { path: '/inventory/reports/price-variances', name: 'inventory-report-price-variances', component: () => import('@/views/inventory/reports/PriceVariancesView.vue'), meta: { title: 'Purchase price variance', permission: ['inventory.view', 'inventory.manage', 'inventory.opening'], module: 'INVENTORY' } },
    { path: '/inventory/reports/transfers-without-requisition', name: 'inventory-report-transfers-without-requisition', component: () => import('@/views/inventory/reports/TransfersNoSrView.vue'), meta: { title: 'Transfers without a requisition', permission: ['inventory.view', 'inventory.manage', 'inventory.opening'], module: 'INVENTORY' } },
    { path: '/inventory/reports/amenities-by-month', name: 'inventory-report-amenities-by-month', component: () => import('@/views/inventory/reports/AmenityMonthsView.vue'), meta: { title: 'Amenities by month', permission: ['inventory.view', 'inventory.manage', 'inventory.opening'], module: 'INVENTORY' } },
    { path: '/inventory/reports/pending-approvals', name: 'inventory-report-pending-approvals', component: () => import('@/views/inventory/reports/PendingApprovalsView.vue'), meta: { title: 'Documents waiting for approval', permission: ['inventory.view', 'inventory.manage', 'inventory.opening'], module: 'INVENTORY' } },
    { path: '/inventory/reports/recipe-costs', name: 'inventory-report-recipe-costs', component: () => import('@/views/inventory/reports/RecipeCostsView.vue'), meta: { title: 'Recipe costs', permission: ['inventory.view', 'inventory.manage', 'inventory.opening'], module: 'INVENTORY' } },
    { path: '/inventory/reports/meals', name: 'inventory-report-meals', component: () => import('@/views/inventory/reports/MealsView.vue'), meta: { title: 'Staff meals and entertainment', permission: ['inventory.view', 'inventory.manage', 'inventory.opening'], module: 'INVENTORY' } },
    { path: '/inventory/reports/item-movements', name: 'inventory-report-item-movements', component: () => import('@/views/inventory/reports/ItemMovementsView.vue'), meta: { title: 'Stock movement per item', permission: ['inventory.view', 'inventory.manage', 'inventory.opening'], module: 'INVENTORY' } },
    { path: '/inventory/reports/purchases', name: 'inventory-report-purchases', component: () => import('@/views/inventory/reports/PurchasesView.vue'), meta: { title: 'Purchases by supplier', permission: ['inventory.view', 'inventory.manage', 'inventory.opening'], module: 'INVENTORY' } },
    { path: '/inventory/reports/price-history', name: 'inventory-report-price-history', component: () => import('@/views/inventory/reports/PriceHistoryView.vue'), meta: { title: 'Purchase price history', permission: ['inventory.view', 'inventory.manage', 'inventory.opening'], module: 'INVENTORY' } },
    { path: '/inventory/reports/slow-stock', name: 'inventory-report-slow-stock', component: () => import('@/views/inventory/reports/SlowStockView.vue'), meta: { title: 'Slow and dead stock', permission: ['inventory.view', 'inventory.manage', 'inventory.opening'], module: 'INVENTORY' } },
    { path: '/inventory/reports/top-usage', name: 'inventory-report-top-usage', component: () => import('@/views/inventory/reports/TopUsageView.vue'), meta: { title: 'Largest usage', permission: ['inventory.view', 'inventory.manage', 'inventory.opening'], module: 'INVENTORY' } },
    { path: '/inventory/items', name: 'inventory-items', component: () => import('@/views/inventory/InventoryItemsView.vue'), meta: { title: 'Items and stock', permission: ['inventory.view', 'inventory.manage', 'inventory.opening'], module: 'INVENTORY' } },
    { path: '/inventory/items/new', name: 'inventory-item-new', component: () => import('@/views/inventory/InventoryItemFormView.vue'), meta: { title: 'New item', permission: 'inventory.manage', module: 'INVENTORY' } },
    { path: '/inventory/items/:id', name: 'inventory-item', component: () => import('@/views/inventory/InventoryItemFormView.vue'), meta: { title: 'Item', permission: 'inventory.manage', module: 'INVENTORY' } },
    { path: '/inventory/openings', name: 'inventory-openings', component: () => import('@/views/inventory/InventoryOpeningsView.vue'), meta: { title: 'Opening stock', permission: ['inventory.view', 'inventory.manage', 'inventory.opening'], module: 'INVENTORY' } },
    { path: '/inventory/openings/new', name: 'inventory-opening-new', component: () => import('@/views/inventory/InventoryOpeningView.vue'), meta: { title: 'Opening stock of a warehouse', permission: 'inventory.opening', module: 'INVENTORY' } },
    { path: '/inventory/openings/:id', name: 'inventory-opening', component: () => import('@/views/inventory/InventoryOpeningView.vue'), meta: { title: 'Opening stock', permission: ['inventory.view', 'inventory.manage', 'inventory.opening'], module: 'INVENTORY' } },
    { path: '/inventory/warehouses', name: 'inventory-warehouses', component: () => import('@/views/inventory/InventoryWarehousesView.vue'), meta: { title: 'Warehouses', permission: ['inventory.view', 'inventory.manage', 'inventory.opening'], module: 'INVENTORY' } },
    { path: '/inventory/warehouses/new', name: 'inventory-warehouse-new', component: () => import('@/views/inventory/InventoryWarehouseFormView.vue'), meta: { title: 'New warehouse', permission: 'inventory.manage', module: 'INVENTORY' } },
    { path: '/inventory/warehouses/:id', name: 'inventory-warehouse', component: () => import('@/views/inventory/InventoryWarehouseFormView.vue'), meta: { title: 'Warehouse', permission: 'inventory.manage', module: 'INVENTORY' } },
    { path: '/inventory/categories', name: 'inventory-categories', component: () => import('@/views/inventory/InventoryCategoriesView.vue'), meta: { title: 'Categories and accounts', permission: ['inventory.view', 'inventory.manage', 'inventory.opening'], module: 'INVENTORY' } },
    { path: '/inventory/categories/new', name: 'inventory-category-new', component: () => import('@/views/inventory/InventoryCategoryFormView.vue'), meta: { title: 'New category', permission: 'inventory.manage', module: 'INVENTORY' } },
    { path: '/inventory/categories/:id', name: 'inventory-category', component: () => import('@/views/inventory/InventoryCategoryFormView.vue'), meta: { title: 'Category', permission: 'inventory.manage', module: 'INVENTORY' } },
    { path: '/inventory/units', name: 'inventory-units', component: () => import('@/views/inventory/InventoryUnitsView.vue'), meta: { title: 'Units', permission: ['inventory.view', 'inventory.manage', 'inventory.opening'], module: 'INVENTORY' } },
    { path: '/inventory/units/new', name: 'inventory-unit-new', component: () => import('@/views/inventory/InventoryUnitFormView.vue'), meta: { title: 'New unit', permission: 'inventory.manage', module: 'INVENTORY' } },
    { path: '/inventory/units/:id', name: 'inventory-unit', component: () => import('@/views/inventory/InventoryUnitFormView.vue'), meta: { title: 'Unit', permission: 'inventory.manage', module: 'INVENTORY' } },
    { path: '/inventory/costing', name: 'inventory-costing', component: () => import('@/views/inventory/InventoryCostingView.vue'), meta: { title: 'Costing', permission: ['inventory.view', 'inventory.manage', 'inventory.opening'], module: 'INVENTORY' } },
    { path: '/inventory/purchasing', name: 'inventory-purchasing', component: () => import('@/views/inventory/purchasing/PurchasingView.vue'), meta: { title: 'Purchasing', permission: ['inventory.view', 'inventory.request', 'inventory.purchase', 'inventory.receive', 'inventory.bill', 'inventory.approve', 'inventory.cost_control', 'inventory.final_approve'], module: 'INVENTORY' } },
    { path: '/inventory/requests/new', name: 'inventory-request-new', component: () => import('@/views/inventory/purchasing/PurchaseRequestView.vue'), meta: { title: 'New purchase request', permission: 'inventory.request', module: 'INVENTORY' } },
    { path: '/inventory/requests/:id', name: 'inventory-request', component: () => import('@/views/inventory/purchasing/PurchaseRequestView.vue'), meta: { title: 'Purchase request', permission: ['inventory.view', 'inventory.request', 'inventory.purchase', 'inventory.receive', 'inventory.bill', 'inventory.approve', 'inventory.cost_control', 'inventory.final_approve'], module: 'INVENTORY' } },
    { path: '/inventory/purchase-orders/new', name: 'inventory-order-new', component: () => import('@/views/inventory/purchasing/PurchaseOrderView.vue'), meta: { title: 'New purchase order', permission: 'inventory.purchase', module: 'INVENTORY' } },
    { path: '/inventory/purchase-orders/:id', name: 'inventory-order', component: () => import('@/views/inventory/purchasing/PurchaseOrderView.vue'), meta: { title: 'Purchase order', permission: ['inventory.view', 'inventory.request', 'inventory.purchase', 'inventory.receive', 'inventory.bill', 'inventory.approve', 'inventory.cost_control', 'inventory.final_approve'], module: 'INVENTORY' } },
    { path: '/inventory/receipts/new', name: 'inventory-receipt-new', component: () => import('@/views/inventory/purchasing/ReceiptView.vue'), meta: { title: 'New goods receipt', permission: 'inventory.receive', module: 'INVENTORY' } },
    { path: '/inventory/receipts/:id', name: 'inventory-receipt', component: () => import('@/views/inventory/purchasing/ReceiptView.vue'), meta: { title: 'Goods receipt', permission: ['inventory.view', 'inventory.request', 'inventory.purchase', 'inventory.receive', 'inventory.bill', 'inventory.approve', 'inventory.cost_control', 'inventory.final_approve'], module: 'INVENTORY' } },
    { path: '/inventory/bill-drafts', name: 'inventory-drafts', component: () => import('@/views/inventory/purchasing/BillDraftsView.vue'), meta: { title: 'Draft bills', permission: ['inventory.bill', 'inventory.view'], module: 'INVENTORY' } },
    { path: '/inventory/bill-drafts/:id', name: 'inventory-draft', component: () => import('@/views/inventory/purchasing/BillDraftView.vue'), meta: { title: 'Draft bill', permission: ['inventory.bill', 'inventory.view'], module: 'INVENTORY' } },
    { path: '/inventory/returns/new', name: 'inventory-return-new', component: () => import('@/views/inventory/purchasing/ReturnView.vue'), meta: { title: 'Return to supplier', permission: 'inventory.receive', module: 'INVENTORY' } },
    { path: '/inventory/returns/:id', name: 'inventory-return', component: () => import('@/views/inventory/purchasing/ReturnView.vue'), meta: { title: 'Return to supplier', permission: ['inventory.view', 'inventory.request', 'inventory.purchase', 'inventory.receive', 'inventory.bill', 'inventory.approve', 'inventory.cost_control', 'inventory.final_approve'], module: 'INVENTORY' } },
    { path: '/inventory/requisitions', name: 'inventory-requisitions', component: () => import('@/views/inventory/movements/MovementsView.vue'), meta: { title: 'Requisitions and transfers', permission: ['inventory.view', 'inventory.request', 'inventory.purchase', 'inventory.receive', 'inventory.move', 'inventory.count', 'inventory.approve', 'inventory.dept_approve', 'inventory.cost_control', 'inventory.final_approve', 'inventory.sr_final_approve'], module: 'INVENTORY' } },
    { path: '/inventory/requisitions/new', name: 'inventory-requisition-new', component: () => import('@/views/inventory/movements/RequisitionView.vue'), meta: { title: 'New store requisition', permission: 'inventory.request', module: 'INVENTORY' } },
    { path: '/inventory/requisitions/:id', name: 'inventory-requisition', component: () => import('@/views/inventory/movements/RequisitionView.vue'), meta: { title: 'Store requisition', permission: ['inventory.view', 'inventory.request', 'inventory.purchase', 'inventory.receive', 'inventory.move', 'inventory.count', 'inventory.approve', 'inventory.dept_approve', 'inventory.cost_control', 'inventory.final_approve', 'inventory.sr_final_approve'], module: 'INVENTORY' } },
    { path: '/inventory/transfers/new', name: 'inventory-transfer-new', component: () => import('@/views/inventory/movements/TransferView.vue'), meta: { title: 'New transfer', permission: 'inventory.move', module: 'INVENTORY' } },
    { path: '/inventory/transfers/:id', name: 'inventory-transfer', component: () => import('@/views/inventory/movements/TransferView.vue'), meta: { title: 'Transfer', permission: ['inventory.view', 'inventory.request', 'inventory.purchase', 'inventory.receive', 'inventory.move', 'inventory.count', 'inventory.approve', 'inventory.dept_approve', 'inventory.cost_control', 'inventory.final_approve', 'inventory.sr_final_approve'], module: 'INVENTORY' } },
    { path: '/inventory/issues', name: 'inventory-issues', component: () => import('@/views/inventory/movements/IssuesView.vue'), meta: { title: 'Use and waste', permission: ['inventory.view', 'inventory.request', 'inventory.purchase', 'inventory.receive', 'inventory.move', 'inventory.count', 'inventory.approve', 'inventory.dept_approve', 'inventory.cost_control', 'inventory.final_approve', 'inventory.sr_final_approve'], module: 'INVENTORY' } },
    { path: '/inventory/issues/new', name: 'inventory-issue-new', component: () => import('@/views/inventory/movements/IssueView.vue'), meta: { title: 'New issue', permission: 'inventory.move', module: 'INVENTORY' } },
    { path: '/inventory/issues/:id', name: 'inventory-issue', component: () => import('@/views/inventory/movements/IssueView.vue'), meta: { title: 'Issue', permission: ['inventory.view', 'inventory.request', 'inventory.purchase', 'inventory.receive', 'inventory.move', 'inventory.count', 'inventory.approve', 'inventory.dept_approve', 'inventory.cost_control', 'inventory.final_approve', 'inventory.sr_final_approve'], module: 'INVENTORY' } },
    { path: '/inventory/counts', name: 'inventory-counts', component: () => import('@/views/inventory/movements/CountsView.vue'), meta: { title: 'Stock counts', permission: ['inventory.view', 'inventory.request', 'inventory.purchase', 'inventory.receive', 'inventory.move', 'inventory.count', 'inventory.approve', 'inventory.dept_approve', 'inventory.cost_control', 'inventory.final_approve', 'inventory.sr_final_approve'], module: 'INVENTORY' } },
    { path: '/inventory/counts/:id', name: 'inventory-count', component: () => import('@/views/inventory/movements/CountView.vue'), meta: { title: 'Stock count', permission: ['inventory.view', 'inventory.request', 'inventory.purchase', 'inventory.receive', 'inventory.move', 'inventory.count', 'inventory.approve', 'inventory.dept_approve', 'inventory.cost_control', 'inventory.final_approve', 'inventory.sr_final_approve'], module: 'INVENTORY' } },
    { path: '/inventory/amenities', name: 'inventory-amenities', component: () => import('@/views/inventory/movements/AmenitiesPlanView.vue'), meta: { title: 'Automatic amenities', permission: ['inventory.view', 'inventory.manage'], module: 'INVENTORY' } },
    { path: '/inventory/amenities/runs/:date', name: 'inventory-amenity-run', component: () => import('@/views/inventory/movements/AmenityRunView.vue'), meta: { title: 'Amenities of a night', permission: ['inventory.view', 'inventory.manage'], module: 'INVENTORY' } },
    { path: '/inventory/pos-gap', name: 'inventory-pos-gap', component: () => import('@/views/inventory/InventoryPosGapView.vue'), meta: { title: 'POS sales without stock deduction', permission: ['inventory.view', 'inventory.manage'], module: 'INVENTORY' } },
    { path: '/inventory/approvals', name: 'inventory-approvals', component: () => import('@/views/inventory/purchasing/ApprovalInboxView.vue'), meta: { title: 'Waiting for my approval', permission: ['inventory.approve', 'inventory.dept_approve', 'inventory.cost_control', 'inventory.final_approve', 'inventory.sr_final_approve'], module: 'INVENTORY' } },
    { path: '/inventory/approval-chains', name: 'inventory-approval-chains', component: () => import('@/views/inventory/purchasing/ApprovalChainsView.vue'), meta: { title: 'Approval chains', permission: ['inventory.manage'], module: 'INVENTORY' } },
    { path: '/outlet-charges', name: 'outlet-charges', component: () => import('@/views/pos/OutletChargesView.vue'), meta: { title: 'Outlet charges', permission: ['folio.outlet_charge_open', 'folio.read'], module: 'POS' } },
    { path: '/accounting/accounts', name: 'accounting-accounts', component: () => import('@/views/accounting/ChartOfAccountsView.vue'), meta: { title: 'Chart of accounts' } },
    { path: '/accounting/mapping', name: 'accounting-mapping', component: () => import('@/views/accounting/AccountMappingView.vue'), meta: { title: 'System accounts' } },
    { path: '/accounting/journals', name: 'accounting-journals', component: () => import('@/views/accounting/JournalsView.vue'), meta: { title: 'Journals' } },
    { path: '/accounting/periods', name: 'accounting-periods', component: () => import('@/views/accounting/PeriodsView.vue'), meta: { title: 'Accounting periods' } },
    { path: '/tax/status', name: 'tax-status', component: () => import('@/views/tax/TaxStatusView.vue'), meta: { title: 'PKP status' } },
    { path: '/tax/profiles', name: 'tax-profiles', component: () => import('@/views/tax/TaxProfilesView.vue'), meta: { title: 'Tax filing profiles' } },
    { path: '/tax/invoices', name: 'tax-invoices', component: () => import('@/views/tax/TaxInvoicesView.vue'), meta: { title: 'Tax invoices' } },
    { path: '/tax/returns', name: 'tax-returns', component: () => import('@/views/tax/TaxReturnsView.vue'), meta: { title: 'Tax returns' } },
    { path: '/tax/liability', name: 'tax-liability', component: () => import('@/views/tax/TaxLiabilityView.vue'), meta: { title: 'Tax owed' } },
    { path: '/bank/accounts', name: 'bank-accounts', component: () => import('@/views/bank/BankAccountsView.vue'), meta: { title: 'Bank accounts' } },
    { path: '/bank/cards', name: 'bank-cards', component: () => import('@/views/bank/BankCardsView.vue'), meta: { title: 'Card settlements' } },
    { path: '/bank/statements', name: 'bank-statements', component: () => import('@/views/bank/BankStatementsView.vue'), meta: { title: 'Bank statements' } },
    { path: '/bank/statements/:id', name: 'bank-reconcile', component: () => import('@/views/bank/BankReconcileView.vue'), props: true, meta: { title: 'Reconcile a statement' } },
    { path: '/payables/suppliers', name: 'payables-suppliers', component: () => import('@/views/payables/SuppliersView.vue'), meta: { title: 'Suppliers' } },
    { path: '/payables/bills', name: 'payables-bills', component: () => import('@/views/payables/BillsView.vue'), meta: { title: 'Supplier bills' } },
    { path: '/payables/credit-notes', name: 'payables-credit-notes', component: () => import('@/views/payables/CreditNotesView.vue'), meta: { title: 'Supplier credit notes' } },
    { path: '/payables/payments', name: 'payables-payments', component: () => import('@/views/payables/PaymentsView.vue'), meta: { title: 'Supplier payments' } },
    { path: '/payables/aging', name: 'payables-aging', component: () => import('@/views/payables/AgingView.vue'), meta: { title: 'Payables aging' } },
    { path: '/accounting/fiscal-years', name: 'accounting-fiscal-years', component: () => import('@/views/accounting/FiscalYearsView.vue'), meta: { title: 'Fiscal years' } },
    { path: '/accounting/departments', name: 'accounting-departments', component: () => import('@/views/accounting/DepartmentsView.vue'), meta: { title: 'Departments' } },
    { path: '/accounting/department-report', name: 'accounting-department-report', component: () => import('@/views/accounting/DepartmentReportView.vue'), meta: { title: 'Department report' } },
    { path: '/accounting/trial-balance', name: 'accounting-trial-balance', component: () => import('@/views/accounting/TrialBalanceView.vue'), meta: { title: 'Trial balance' } },
    { path: '/accounting/ledger', name: 'accounting-ledger', component: () => import('@/views/accounting/GeneralLedgerView.vue'), meta: { title: 'General ledger' } },
    { path: '/accounting/income-statement', name: 'accounting-income-statement', component: () => import('@/views/accounting/StatementView.vue'), meta: { title: 'Income statement' } },
    { path: '/accounting/balance-sheet', name: 'accounting-balance-sheet', component: () => import('@/views/accounting/StatementView.vue'), meta: { title: 'Balance sheet' } },
    { path: '/accounting/cash-flow', name: 'accounting-cash-flow', component: () => import('@/views/accounting/StatementView.vue'), meta: { title: 'Cash flow statement' } },
    { path: '/accounting/reconciliation', name: 'accounting-reconciliation', component: () => import('@/views/accounting/ReconciliationView.vue'), meta: { title: 'Control accounts' } },
    { path: '/budget', name: 'budget', component: () => import('@/views/budget/BudgetsView.vue'), meta: { title: 'Budgets' } },
    { path: '/budget/vs-actual', name: 'budget-vs-actual', component: () => import('@/views/budget/BudgetVsActualView.vue'), meta: { title: 'Budget against actual' } },
    { path: '/budget/:id', name: 'budget-editor', component: () => import('@/views/budget/BudgetEditorView.vue'), meta: { title: 'Budget' } },
    {
      path: '/room-blocks',
      name: 'room-blocks',
      component: () => import('@/views/rooms/RoomBlocksView.vue'),
      meta: { title: 'Room blocks' },
    },
    {
      path: '/setup/room-types',
      name: 'room-types',
      component: () => import('@/views/rooms/RoomTypesView.vue'),
      meta: { title: 'Room types', permission: 'room.manage' },
    },
    { path: '/setup/approval-rules', name: 'approval-rules', component: () => import('@/views/setup/ApprovalRulesView.vue'), meta: { title: 'Approval rules', permission: 'property.manage' } },
    { path: '/setup/free-quotas', name: 'free-quotas', component: () => import('@/views/rates/FreeNightQuotasView.vue'), meta: { title: 'Free night quotas', permission: 'rate.manage' } },
    { path: '/setup/bed-types', name: 'bed-types', component: () => import('@/views/rooms/BedTypesView.vue'), meta: { title: 'Bed types', permission: 'room.manage' } },
    { path: '/setup/rooms', name: 'rooms', component: () => import('@/views/rooms/RoomsView.vue'), meta: { title: 'Rooms', permission: 'room.manage' } },
    { path: '/guests', name: 'guests', component: () => import('@/views/guests/GuestsView.vue'), meta: { title: 'Guests' } },
    {
      path: '/guests/:id(\\d+)',
      name: 'guest',
      component: () => import('@/views/guests/GuestDetailView.vue'),
      props: true,
      meta: { title: 'Guest' },
    },
    { path: '/setup/taxes', name: 'taxes', component: () => import('@/views/billing/TaxesView.vue'), meta: { title: 'Taxes and service charges', permission: 'billing_config.manage' } },
    { path: '/setup/charge-codes', name: 'charge-codes', component: () => import('@/views/billing/ChargeCodesView.vue'), meta: { title: 'Charge codes', permission: 'billing_config.manage' } },
    { path: '/setup/rate-plans', name: 'rate-plans', component: () => import('@/views/rates/RatePlansView.vue'), meta: { title: 'Rate plans', permission: 'rate.manage' } },
    { path: '/setup/bed-supplements', name: 'bed-supplements', component: () => import('@/views/rates/BedSupplementsView.vue'), meta: { title: 'Bed supplements', permission: 'rate.manage' } },
    { path: '/setup/restrictions', name: 'restrictions', component: () => import('@/views/rates/RestrictionsView.vue'), meta: { title: 'Restrictions', permission: 'rate.manage' } },
    { path: '/setup/yield-rules', name: 'yield-rules', component: () => import('@/views/rates/YieldRulesView.vue'), meta: { title: 'Yield rules', permission: 'rate.manage' } },
    { path: '/setup/rates', name: 'rate-grid', component: () => import('@/views/rates/RateGridView.vue'), meta: { title: 'Rate grid', permission: 'rate.manage' } },
    { path: '/setup/companies', name: 'companies', component: () => import('@/views/accounts/CompaniesView.vue'), meta: { title: 'Companies', permission: 'company.manage' } },
    { path: '/groups', name: 'groups', component: () => import('@/views/accounts/GroupsView.vue'), meta: { title: 'Groups' } },
    {
      path: '/groups/:id(\\d+)',
      name: 'group',
      component: () => import('@/views/accounts/GroupDetailView.vue'),
      props: true,
      meta: { title: 'Group' },
    },
    { path: '/city-ledger', name: 'city-ledger', component: () => import('@/views/accounts/CityLedgerView.vue'), meta: { title: 'City ledger' } },
    { path: '/city-ledger/overdue', name: 'city-ledger-overdue', component: () => import('@/views/accounts/CityLedgerOverdueView.vue'), meta: { title: 'Overdue invoices' } },
    {
      path: '/city-ledger/:id(\\d+)',
      name: 'city-ledger-account',
      component: () => import('@/views/accounts/CityLedgerAccountView.vue'),
      props: true,
      meta: { title: 'Company account' },
    },
    { path: '/reservations', name: 'reservations', component: () => import('@/views/reservations/ReservationsView.vue'), meta: { title: 'Reservations' } },
    { path: '/reservations/new', name: 'reservation-new', component: () => import('@/views/reservations/NewReservationView.vue'), meta: { title: 'New reservation' } },
    { path: '/availability', name: 'availability', component: () => import('@/views/reservations/AvailabilityCalendarView.vue'), meta: { title: 'Availability' } },
    { path: '/reservations/tape', name: 'tape-chart', component: () => import('@/views/reservations/TapeChartView.vue'), meta: { title: 'Tape chart' } },
    {
      path: '/reservations/:id(\\d+)',
      name: 'reservation',
      component: () => import('@/views/reservations/ReservationDetailView.vue'),
      props: true,
      meta: { title: 'Reservation' },
    },
    { path: '/folios', name: 'folios', component: () => import('@/views/billing/FoliosView.vue'), meta: { title: 'Folios' } },
    {
      path: '/folios/:id(\\d+)',
      name: 'folio',
      component: () => import('@/views/billing/FolioView.vue'),
      props: true,
      meta: { title: 'Folio' },
    },
    { path: '/night-audit', name: 'night-audit', component: () => import('@/views/nightaudit/NightAuditView.vue'), meta: { title: 'Night audit' } },
    { path: '/audit', name: 'audit', component: () => import('@/views/audit/AuditTrailView.vue'), meta: { title: 'Audit trail' } },
    { path: '/performance', name: 'performance', component: () => import('@/views/dashboard/PerformanceView.vue'), meta: { title: 'Performance' } },
    { path: '/reports', name: 'reports', component: () => import('@/views/reports/ReportsView.vue'), meta: { title: 'Reports' } },
    { path: '/room-charges', name: 'room-charges', component: () => import('@/views/billing/RoomChargesView.vue'), meta: { title: 'Room charges' } },
    { path: '/cashier/shifts', name: 'cashier-shifts', component: () => import('@/views/billing/CashierShiftView.vue'), meta: { title: 'Cashier shifts' } },
    { path: '/cashier', name: 'cashier', component: () => import('@/views/billing/CashierView.vue'), meta: { title: 'Cashier' } },
    { path: '/arrivals', name: 'arrivals', component: () => import('@/views/frontdesk/FrontDeskView.vue'), props: { tab: 'arrivals' }, meta: { title: 'Arrivals' } },
    { path: '/walk-in', name: 'walk-in', component: () => import('@/views/frontdesk/WalkInView.vue'), meta: { title: 'Walk-in' } },
    { path: '/in-house', name: 'in-house', component: () => import('@/views/frontdesk/FrontDeskView.vue'), props: { tab: 'in-house' }, meta: { title: 'In-house' } },
    { path: '/departures', name: 'departures', component: () => import('@/views/frontdesk/FrontDeskView.vue'), props: { tab: 'departures' }, meta: { title: 'Departures' } },
    {
      path: '/stays/:id(\\d+)',
      name: 'stay',
      component: () => import('@/views/frontdesk/StayDetailView.vue'),
      props: true,
      meta: { title: 'Stay' },
    },
    { path: '/room-status', name: 'room-status', component: () => import('@/views/rooms/RoomStatusView.vue'), meta: { title: 'Room status' } },
    { path: '/account', name: 'account', component: () => import('@/views/AccountView.vue'), meta: { title: 'Account' } },
    {
      path: '/setup/properties',
      name: 'properties',
      component: () => import('@/views/setup/PropertiesView.vue'),
      meta: { title: 'Properties', adminOnly: true },
    },
    {
      path: '/setup/properties/new',
      name: 'property-new',
      component: () => import('@/views/setup/PropertyFormView.vue'),
      meta: { title: 'New property', adminOnly: true },
    },
    {
      path: '/setup/properties/:id(\\d+)',
      name: 'property-edit',
      component: () => import('@/views/setup/PropertyFormView.vue'),
      props: true,
      meta: { title: 'Property', permission: 'property.manage' },
    },
    { path: '/setup/users', name: 'users', component: () => import('@/views/setup/UsersView.vue'), meta: { title: 'Users', adminOnly: true } },
    {
      path: '/setup/users/new',
      name: 'user-new',
      component: () => import('@/views/setup/UserFormView.vue'),
      meta: { title: 'New user', adminOnly: true },
    },
    {
      path: '/setup/users/:id(\\d+)',
      name: 'user-edit',
      component: () => import('@/views/setup/UserFormView.vue'),
      props: true,
      meta: { title: 'User', adminOnly: true },
    },
    { path: '/setup/roles', name: 'roles', component: () => import('@/views/setup/RolesView.vue'), meta: { title: 'Roles', adminOnly: true } },
    {
      path: '/setup/roles/new',
      name: 'role-new',
      component: () => import('@/views/setup/RoleFormView.vue'),
      meta: { title: 'New role', adminOnly: true },
    },
    {
      path: '/setup/roles/:id(\\d+)',
      name: 'role-edit',
      component: () => import('@/views/setup/RoleFormView.vue'),
      props: true,
      meta: { title: 'Role', adminOnly: true },
    },
    {
      path: '/:pathMatch(.*)*',
      name: 'not-found',
      component: () => import('@/views/NotFoundView.vue'),
      meta: { title: 'Not found' },
    },
  ],
})

// Every page needs a signed-in user except those marked public. The UI check is a
// convenience: the API enforces the same rules on every request.
router.beforeEach(async (to) => {
  const auth = useAuthStore()
  await auth.init()
  if (to.meta.public) {
    return auth.isAuthenticated && to.name === 'login' ? { name: 'home' } : true
  }
  if (!auth.isAuthenticated) {
    return { name: 'login', query: to.fullPath === '/' ? {} : { redirect: to.fullPath } }
  }
  if (to.meta.adminOnly && !auth.isAdmin) return { name: 'home' }
  // A page of a module that is not in the plan of the tenant goes home (the server refuses its routes in any case).
  if (to.meta.module) {
    const subscription = useSubscriptionStore()
    await subscription.load()
    if (!subscription.has(to.meta.module)) return { name: 'home' }
  }
  // A page of setup needs the permission that manages what it shows: the menu leaves it out, and a typed address goes home.
  if (to.meta.permission) {
    const property = usePropertyStore()
    if (!property.loaded) await property.loadProperties()
    if (![to.meta.permission].flat().some((p) => auth.can(p, property.currentId))) return { name: 'home' }
  }
  return true
})

// The title of the tab follows the page and the language.
const setTitle = (to: Parameters<typeof routeTitle>[0]): void => {
  const title = routeTitle(to)
  document.title = title ? `${title} · KamaraPMS` : 'KamaraPMS'
}
watch(() => i18n.global.locale.value, () => setTitle(router.currentRoute.value))
router.afterEach((to) => {
  setTitle(to)
})
