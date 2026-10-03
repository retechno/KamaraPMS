import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import HomeView from '@/views/HomeView.vue'

declare module 'vue-router' {
  interface RouteMeta {
    title?: string
    public?: boolean // reachable without signing in
    adminOnly?: boolean // tenant administrators only
  }
}

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', name: 'login', component: () => import('@/views/LoginView.vue'), meta: { title: 'Sign in', public: true } },
    { path: '/', name: 'home', component: HomeView, meta: { title: 'Dashboard' } },
    {
      path: '/housekeeping',
      name: 'housekeeping',
      component: () => import('@/views/rooms/HousekeepingView.vue'),
      meta: { title: 'Housekeeping' },
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
    { path: '/accounting/accounts', name: 'accounting-accounts', component: () => import('@/views/accounting/ChartOfAccountsView.vue'), meta: { title: 'Chart of accounts' } },
    { path: '/accounting/mapping', name: 'accounting-mapping', component: () => import('@/views/accounting/AccountMappingView.vue'), meta: { title: 'System accounts' } },
    { path: '/accounting/journals', name: 'accounting-journals', component: () => import('@/views/accounting/JournalsView.vue'), meta: { title: 'Journals' } },
    { path: '/accounting/periods', name: 'accounting-periods', component: () => import('@/views/accounting/PeriodsView.vue'), meta: { title: 'Accounting periods' } },
    { path: '/tax/profiles', name: 'tax-profiles', component: () => import('@/views/tax/TaxProfilesView.vue'), meta: { title: 'Tax filing profiles' } },
    { path: '/tax/returns', name: 'tax-returns', component: () => import('@/views/tax/TaxReturnsView.vue'), meta: { title: 'Tax returns' } },
    { path: '/tax/liability', name: 'tax-liability', component: () => import('@/views/tax/TaxLiabilityView.vue'), meta: { title: 'Tax owed' } },
    { path: '/bank/accounts', name: 'bank-accounts', component: () => import('@/views/bank/BankAccountsView.vue'), meta: { title: 'Bank accounts' } },
    { path: '/bank/statements', name: 'bank-statements', component: () => import('@/views/bank/BankStatementsView.vue'), meta: { title: 'Bank statements' } },
    { path: '/bank/statements/:id', name: 'bank-reconcile', component: () => import('@/views/bank/BankReconcileView.vue'), props: true, meta: { title: 'Reconcile a statement' } },
    { path: '/payables/suppliers', name: 'payables-suppliers', component: () => import('@/views/payables/SuppliersView.vue'), meta: { title: 'Suppliers' } },
    { path: '/payables/bills', name: 'payables-bills', component: () => import('@/views/payables/BillsView.vue'), meta: { title: 'Supplier bills' } },
    { path: '/payables/payments', name: 'payables-payments', component: () => import('@/views/payables/PaymentsView.vue'), meta: { title: 'Supplier payments' } },
    { path: '/payables/aging', name: 'payables-aging', component: () => import('@/views/payables/AgingView.vue'), meta: { title: 'Payables aging' } },
    { path: '/accounting/fiscal-years', name: 'accounting-fiscal-years', component: () => import('@/views/accounting/FiscalYearsView.vue'), meta: { title: 'Fiscal years' } },
    { path: '/accounting/trial-balance', name: 'accounting-trial-balance', component: () => import('@/views/accounting/TrialBalanceView.vue'), meta: { title: 'Trial balance' } },
    { path: '/accounting/ledger', name: 'accounting-ledger', component: () => import('@/views/accounting/GeneralLedgerView.vue'), meta: { title: 'General ledger' } },
    { path: '/accounting/income-statement', name: 'accounting-income-statement', component: () => import('@/views/accounting/StatementView.vue'), meta: { title: 'Income statement' } },
    { path: '/accounting/balance-sheet', name: 'accounting-balance-sheet', component: () => import('@/views/accounting/StatementView.vue'), meta: { title: 'Balance sheet' } },
    { path: '/accounting/reconciliation', name: 'accounting-reconciliation', component: () => import('@/views/accounting/ReconciliationView.vue'), meta: { title: 'Control accounts' } },
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
      meta: { title: 'Room types' },
    },
    { path: '/setup/bed-types', name: 'bed-types', component: () => import('@/views/rooms/BedTypesView.vue'), meta: { title: 'Bed types' } },
    { path: '/setup/rooms', name: 'rooms', component: () => import('@/views/rooms/RoomsView.vue'), meta: { title: 'Rooms' } },
    { path: '/guests', name: 'guests', component: () => import('@/views/guests/GuestsView.vue'), meta: { title: 'Guests' } },
    {
      path: '/guests/:id(\\d+)',
      name: 'guest',
      component: () => import('@/views/guests/GuestDetailView.vue'),
      props: true,
      meta: { title: 'Guest' },
    },
    { path: '/setup/taxes', name: 'taxes', component: () => import('@/views/billing/TaxesView.vue'), meta: { title: 'Taxes and service charges' } },
    { path: '/setup/charge-codes', name: 'charge-codes', component: () => import('@/views/billing/ChargeCodesView.vue'), meta: { title: 'Charge codes' } },
    { path: '/setup/rate-plans', name: 'rate-plans', component: () => import('@/views/rates/RatePlansView.vue'), meta: { title: 'Rate plans' } },
    { path: '/setup/yield-rules', name: 'yield-rules', component: () => import('@/views/rates/YieldRulesView.vue'), meta: { title: 'Yield rules' } },
    { path: '/setup/rates', name: 'rate-grid', component: () => import('@/views/rates/RateGridView.vue'), meta: { title: 'Rate grid' } },
    { path: '/setup/companies', name: 'companies', component: () => import('@/views/accounts/CompaniesView.vue'), meta: { title: 'Companies' } },
    { path: '/groups', name: 'groups', component: () => import('@/views/accounts/GroupsView.vue'), meta: { title: 'Groups' } },
    {
      path: '/groups/:id(\\d+)',
      name: 'group',
      component: () => import('@/views/accounts/GroupDetailView.vue'),
      props: true,
      meta: { title: 'Group' },
    },
    { path: '/city-ledger', name: 'city-ledger', component: () => import('@/views/accounts/CityLedgerView.vue'), meta: { title: 'City ledger' } },
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
    { path: '/reports', name: 'reports', component: () => import('@/views/reports/ReportsView.vue'), meta: { title: 'Reports' } },
    { path: '/room-charges', name: 'room-charges', component: () => import('@/views/billing/RoomChargesView.vue'), meta: { title: 'Room charges' } },
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
      meta: { title: 'Property' },
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
  return true
})

router.afterEach((to) => {
  document.title = to.meta.title ? `${to.meta.title} · KamaraPMS` : 'KamaraPMS'
})
