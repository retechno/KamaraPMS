import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { setLocale } from '@/i18n'
import TenantView from './TenantView.vue'
import { invoice, plan, plans, tenant } from './tenantFixture'

let GET = vi.fn()
vi.mock('@/api/platformClient', () => ({ platformApi: { GET: (...a: unknown[]) => GET(...a) } }))

type Served = { tenant?: unknown; invoices?: unknown; admins?: unknown; activity?: unknown; plans?: unknown }
const apiError = (status: number, code: string, title: string) => new ApiError({ type: 'x', title, status, code })

/** Answers each endpoint of the page with its data, or throws the Error it is given. */
function serve(s: Served = {}) {
  const data: Required<Served> = {
    tenant: tenant(),
    invoices: { items: [] },
    admins: { items: [] },
    activity: { items: [] },
    plans: { items: plans },
    ...s,
  }
  const pick: Record<string, unknown> = {
    '/api/v1/platform/tenants/{code}': data.tenant,
    '/api/v1/platform/tenants/{code}/invoices': data.invoices,
    '/api/v1/platform/tenants/{code}/admins': data.admins,
    '/api/v1/platform/tenants/{code}/activity': data.activity,
    '/api/v1/platform/plans': data.plans,
  }
  GET = vi.fn(async (url: string) => {
    const v = pick[url]
    if (v instanceof Error) throw v
    return { data: v }
  })
}

// The dialogs are tested on their own: here they are marks that show whether they are open and with what.
const stub = (name: string) =>
  defineComponent({
    name,
    props: { open: Boolean, tenant: Object, tenantCode: String, currentPlan: String, tenantName: String, mode: String, focus: String, invoice: Object, plans: Array },
    emits: ['update:open', 'saved', 'issued', 'done', 'paid', 'voided'],
    setup: (p) => () => (p.open ? h('div', { 'data-testid': `stub-${name}`, 'data-focus': p.focus, 'data-mode': p.mode, 'data-code': p.tenantCode, 'data-plan': p.currentPlan, 'data-invoice': (p.invoice as { number?: string } | undefined)?.number }) : null),
  })
const STUBS = Object.fromEntries(['ChangeSubscriptionDialog', 'IssueInvoiceDialog', 'SuspendTenantDialog', 'RecordPaymentDialog', 'VoidInvoiceDialog'].map((n) => [n, stub(n)]))

async function mountView(code = 'SGR') {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/platform/tenants', name: 'platform-tenants', component: { template: '<div />' } },
      { path: '/platform/tenants/:code', name: 'platform-tenant', component: TenantView },
    ],
  })
  await router.push(`/platform/tenants/${code}`)
  const w = mount(TenantView, { props: { code }, global: { plugins: [router], stubs: STUBS } })
  await flushPromises()
  return w
}
const tid = (w: VueWrapper, id: string) => w.get(`[data-testid=${id}]`)
const has = (w: VueWrapper, id: string) => w.find(`[data-testid=${id}]`).exists()
const calls = (url: string) => GET.mock.calls.filter((c) => c[0] === url)

const graceTenant = () => tenant({ summary: { access: 'GRACE', paid_through: '2026-11-08', grace_ends_on: '2026-11-22', days_left: 8 } })

describe('TenantView', () => {
  beforeEach(async () => {
    serve()
    await setLocale('en')
  })

  describe('loading and failing', () => {
    it('shows a skeleton and the code as the title while it loads', async () => {
      GET = vi.fn(() => new Promise(() => {}))
      const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: TenantView }, { path: '/platform/tenants', name: 'platform-tenants', component: { template: '<div />' } }] })
      await router.push('/')
      const w = mount(TenantView, { props: { code: 'SGR' }, global: { plugins: [router], stubs: STUBS } })
      expect(has(w, 'tenant-loading')).toBe(true)
      expect(w.get('h1').text()).toBe('SGR')
      expect(has(w, 'action-change')).toBe(false)
    })

    it('says that the tenant does not exist and offers the way back', async () => {
      serve({ tenant: apiError(404, 'TENANT_NOT_FOUND', 'No tenant with this code.') })
      const w = await mountView('NOPE')
      expect(tid(w, 'tenant-error').text()).toContain('No tenant with this code.')
      expect(tid(w, 'tenant-back').attributes('href')).toBe('/platform/tenants')
      expect(w.findAll('h1')).toHaveLength(1)
      expect(has(w, 'action-change')).toBe(false)
      expect(has(w, 'tenant-subscription')).toBe(false)
    })

    it('shows any other failure inline', async () => {
      serve({ tenant: apiError(500, 'INTERNAL', 'Something broke.') })
      const w = await mountView()
      expect(tid(w, 'tenant-error').text()).toContain('Something broke.')
    })

    it('reads the tenant, its invoices, administrators, latest activity and the plans', async () => {
      await mountView()
      expect(GET).toHaveBeenCalledWith('/api/v1/platform/tenants/{code}', { params: { path: { code: 'SGR' } } })
      expect(GET).toHaveBeenCalledWith('/api/v1/platform/tenants/{code}/invoices', { params: { path: { code: 'SGR' } } })
      expect(GET).toHaveBeenCalledWith('/api/v1/platform/tenants/{code}/admins', { params: { path: { code: 'SGR' } } })
      expect(GET).toHaveBeenCalledWith('/api/v1/platform/tenants/{code}/activity', { params: { path: { code: 'SGR' }, query: { limit: 5 } } })
      expect(GET).toHaveBeenCalledWith('/api/v1/platform/plans')
    })
  })

  describe('the head', () => {
    it('has the breadcrumb, the name as the only h1, the status and the meta line', async () => {
      const w = await mountView()
      expect(tid(w, 'tenant-breadcrumb').text()).toBe('Tenants / SGR')
      expect(tid(w, 'tenant-breadcrumb').get('a').attributes('href')).toBe('/platform/tenants')
      expect(w.findAll('h1')).toHaveLength(1)
      expect(w.get('h1').text()).toBe('Segara Resort Group')
      expect(tid(w, 'access-badge').text()).toBe('Active')
      expect(w.get('[data-slot=page-header]').text()).toContain('Code SGR · Asia/Makassar · tenant since 2 Mar 2026 · 3 properties')
    })

    it('says "1 property" for one', async () => {
      serve({ tenant: { ...tenant(), property_count: 1 } })
      const w = await mountView()
      expect(w.get('[data-slot=page-header]').text()).toContain('· 1 property')
    })

    it('offers change, issue and suspend for an active tenant', async () => {
      const w = await mountView()
      expect(tid(w, 'action-change').text()).toBe('Change subscription')
      expect(tid(w, 'action-issue').text()).toBe('Issue invoice')
      expect(tid(w, 'action-suspend').text()).toBe('Suspend')
      expect(has(w, 'action-reactivate')).toBe(false)
    })

    it('shows a suspended tenant with its reason and "Reactivate" instead of "Suspend"', async () => {
      serve({ tenant: tenant({ status: 'SUSPENDED', reason: 'Tenant asked to pause' }) })
      const w = await mountView()
      expect(tid(w, 'tenant-status').text()).toBe('Suspended')
      expect(has(w, 'access-badge')).toBe(false)
      expect(tid(w, 'tenant-callout').attributes('data-kind')).toBe('danger')
      expect(tid(w, 'tenant-callout').text()).toContain('Tenant asked to pause')
      expect(tid(w, 'action-reactivate').text()).toBe('Reactivate')
      expect(has(w, 'action-suspend')).toBe(false)
    })
  })

  describe('the status callout', () => {
    it('is not there for a tenant that is paid up', async () => {
      const w = await mountView()
      expect(has(w, 'tenant-callout')).toBe(false)
    })

    it('says when a trial is ending', async () => {
      serve({ tenant: tenant({ subscription: { status: 'TRIAL' }, summary: { status: 'TRIAL', is_trial_ending: true, days_left: 3, paid_through: '2026-11-18' } }) })
      const w = await mountView()
      expect(tid(w, 'access-badge').text()).toBe('Trial')
      expect(tid(w, 'tenant-callout').text()).toContain('The trial ends 18 Nov 2026 (3 days left).')
    })

    it('is not there for a trial that is not ending', async () => {
      serve({ tenant: tenant({ subscription: { status: 'TRIAL' }, summary: { status: 'TRIAL', days_left: 12 } }) })
      const w = await mountView()
      expect(has(w, 'tenant-callout')).toBe(false)
    })

    it('says how long the grace period lasts, names the open invoice and records its payment', async () => {
      serve({ tenant: graceTenant(), invoices: { items: [invoice({ is_overdue: true })] } })
      const w = await mountView()
      expect(tid(w, 'access-badge').text()).toBe('Grace period')
      const callout = tid(w, 'tenant-callout')
      expect(callout.attributes('data-kind')).toBe('warning')
      expect(callout.text()).toContain('Paid through 8 Nov 2026. The grace period ends 22 Nov 2026 (8 days left)')
      expect(callout.text()).toContain('Invoice SUB-2026-00041 of IDR 7,500,000 is not paid.')
      await tid(w, 'callout-pay').trigger('click')
      expect(tid(w, 'stub-RecordPaymentDialog').attributes('data-invoice')).toBe('SUB-2026-00041')
    })

    it('points the callout and "Record payment" at the issued invoice whose period starts first, not the first of the list', async () => {
      serve({
        tenant: graceTenant(),
        invoices: { items: [invoice({ id: 43, number: 'SUB-2026-00043', period_start: '2026-12-09', period_end: '2027-03-08' }), invoice({ id: 42, number: 'SUB-2026-00042', period_start: '2026-09-09', period_end: '2026-12-08', is_overdue: true })] },
      })
      const w = await mountView()
      expect(tid(w, 'tenant-callout').text()).toContain('Invoice SUB-2026-00042 of')
      expect(tid(w, 'tenant-callout').text()).not.toContain('SUB-2026-00043')
      await tid(w, 'callout-pay').trigger('click')
      expect(tid(w, 'stub-RecordPaymentDialog').attributes('data-invoice')).toBe('SUB-2026-00042')
    })

    it('has no "Record payment" when no invoice is open', async () => {
      serve({ tenant: graceTenant(), invoices: { items: [invoice({ status: 'PAID', paid_on: '2026-11-01' })] } })
      const w = await mountView()
      expect(has(w, 'tenant-callout')).toBe(true)
      expect(has(w, 'callout-pay')).toBe(false)
      expect(tid(w, 'tenant-callout').text()).not.toContain('is not paid')
    })

    it('says that a hotel that is read only can only read', async () => {
      serve({ tenant: tenant({ summary: { access: 'READ_ONLY', paid_through: '2026-10-01', grace_ends_on: '2026-10-15', days_left: 0 } }), invoices: { items: [invoice()] } })
      const w = await mountView()
      expect(tid(w, 'access-badge').text()).toBe('Read only')
      expect(tid(w, 'tenant-callout').attributes('data-kind')).toBe('danger')
      expect(tid(w, 'tenant-callout').text()).toContain('The grace period ended 15 Oct 2026: the hotel can only read its data.')
      expect(has(w, 'callout-pay')).toBe(true)
    })
  })

  describe('the subscription', () => {
    it('lists plan, status, start, paid through, grace days and the price of the period', async () => {
      const w = await mountView()
      expect(tid(w, 'fact-plan').text()).toBe('PRO · Pro')
      expect(tid(w, 'fact-status').text()).toBe('Active')
      expect(tid(w, 'fact-started').text()).toBe('2 Mar 2026')
      expect(tid(w, 'fact-paid-through').text()).toBe('8 Feb 2027')
      expect(tid(w, 'fact-grace-days').text()).toBe('14 days')
      expect(tid(w, 'fact-billing').text()).toBe('IDR 7,500,000 / 3 months')
    })

    it('says "No end date" for a subscription without an end, and "Trial" for a trial', async () => {
      serve({ tenant: tenant({ subscription: { status: 'TRIAL', paid_through: null }, summary: { status: 'TRIAL', paid_through: null, days_left: null } }) })
      const w = await mountView()
      expect(tid(w, 'fact-paid-through').text()).toBe('No end date')
      expect(tid(w, 'fact-status').text()).toBe('Trial')
    })

    it('leaves the price out when the plans cannot be read, and the rest still shows', async () => {
      serve({ plans: apiError(500, 'INTERNAL', 'Something broke.') })
      const w = await mountView()
      expect(has(w, 'fact-billing')).toBe(false)
      expect(tid(w, 'fact-plan').text()).toBe('PRO · Pro')
    })

    it('opens the change dialog from the card, without the quotas', async () => {
      const w = await mountView()
      await tid(w, 'subscription-change').trigger('click')
      expect(tid(w, 'stub-ChangeSubscriptionDialog').attributes('data-focus')).toBeUndefined()
    })
  })

  describe('quotas and usage', () => {
    it('shows a meter for each resource, with no mark when nothing is overridden', async () => {
      const w = await mountView()
      expect(tid(w, 'quota-properties').text()).toContain('3')
      expect(tid(w, 'quota-rooms').text()).toContain('100')
      expect(tid(w, 'quota-rooms').text()).toContain('120')
      expect(tid(w, 'quota-users').text()).toContain('25')
      expect(has(w, 'override-rooms')).toBe(false)
    })

    it('marks a quota that is the tenant\'s own, with the limit of the plan', async () => {
      serve({
        tenant: tenant({
          subscription: { overrides: { max_properties: null, max_rooms: 150, max_users: null } },
          summary: { limits: { max_properties: 3, max_rooms: 150, max_users: 30 } },
        }),
      })
      const w = await mountView()
      expect(tid(w, 'override-rooms').text()).toBe('Tenant-specific (plan: 120)')
      expect(has(w, 'override-users')).toBe(false)
      expect(tid(w, 'quota-rooms').text()).toContain('150')
    })

    it('says "unlimited" when the plan has no limit and the tenant has one', async () => {
      serve({
        plans: { items: [plan({ limits: { max_properties: null, max_rooms: null, max_users: null } })] },
        tenant: tenant({ subscription: { overrides: { max_properties: null, max_rooms: 150, max_users: null } }, summary: { limits: { max_properties: null, max_rooms: 150, max_users: null } } }),
      })
      const w = await mountView()
      expect(tid(w, 'override-rooms').text()).toBe('Tenant-specific (plan: unlimited)')
    })

    it('shows a quota that is used up', async () => {
      serve({ tenant: tenant({ summary: { usage: { properties: 3, rooms: 120, users: 25 } } }) })
      const w = await mountView()
      expect(tid(w, 'quota-rooms').attributes('data-level')).toBe('full')
    })

    it('opens the change dialog at the quotas with "Set quotas"', async () => {
      const w = await mountView()
      await tid(w, 'quotas-set').trigger('click')
      expect(tid(w, 'stub-ChangeSubscriptionDialog').attributes('data-focus')).toBe('quotas')
    })
  })

  describe('modules', () => {
    it('shows the six tiles: the core always, the others by the plan', async () => {
      const w = await mountView()
      expect(w.findAll('[data-testid=tenant-modules] li')).toHaveLength(6)
      expect(tid(w, 'module-CORE').text()).toContain('always')
      for (const m of ['HOUSEKEEPING', 'ACCOUNTING', 'POS']) expect(tid(w, `module-${m}`).attributes('data-included')).toBe('true')
      for (const m of ['INVENTORY', 'BANQUET']) {
        expect(tid(w, `module-${m}`).attributes('data-included')).toBe('false')
        expect(tid(w, `module-${m}`).text()).toContain('not included')
      }
    })
  })

  describe('invoices', () => {
    const rows = {
      items: [
        invoice({ is_overdue: true }),
        invoice({ id: 27, number: 'SUB-2026-00027', status: 'PAID', paid_on: '2026-08-07', payment_reference: 'TRF-88213', period_start: '2026-08-09', period_end: '2026-11-08', due_on: '2026-08-09' }),
        invoice({ id: 11, number: 'SUB-2026-00011', status: 'VOID', void_reason: 'Wrong price', amount: '8250000' }),
        invoice({ id: 12, number: 'SUB-2026-00012', status: 'ISSUED', is_overdue: false }),
      ],
    }

    it('lists them with the period, amount, state and due date', async () => {
      serve({ invoices: rows })
      const w = await mountView()
      const first = tid(w, 'invoice-SUB-2026-00041')
      expect(first.text()).toContain('9 Nov 2026 – 8 Feb 2027')
      expect(first.text()).toContain('IDR 7,500,000')
      expect(first.text()).toContain('Overdue')
      expect(tid(w, 'invoice-SUB-2026-00012').text()).toContain('Issued')
      expect(tid(w, 'invoice-SUB-2026-00027').text()).toContain('Paid')
      expect(tid(w, 'invoice-SUB-2026-00011').text()).toContain('Voided')
      expect(tid(w, 'invoice-SUB-2026-00011').text()).toContain('IDR 8,250,000')
    })

    it('has Record payment and Void on an issued invoice only; paid shows date and reference, voided the reason', async () => {
      serve({ invoices: rows })
      const w = await mountView()
      for (const n of ['SUB-2026-00041', 'SUB-2026-00012']) {
        const row = tid(w, `invoice-${n}`)
        expect(row.find('[data-testid=invoice-pay]').exists()).toBe(true)
        expect(row.find('[data-testid=invoice-void]').exists()).toBe(true)
      }
      const paid = tid(w, 'invoice-SUB-2026-00027')
      expect(paid.find('[data-testid=invoice-pay]').exists()).toBe(false)
      expect(paid.get('[data-testid=invoice-note]').text()).toBe('Paid 7 Aug 2026 · TRF-88213')
      const voided = tid(w, 'invoice-SUB-2026-00011')
      expect(voided.find('[data-testid=invoice-void]').exists()).toBe(false)
      expect(voided.get('[data-testid=invoice-note]').text()).toBe('Voided: Wrong price')
    })

    it('says "Paid" alone when there is no reference', async () => {
      serve({ invoices: { items: [invoice({ status: 'PAID', paid_on: '2026-08-07' })] } })
      const w = await mountView()
      expect(tid(w, 'invoice-note').text()).toBe('Paid 7 Aug 2026')
    })

    it('says when there are none', async () => {
      const w = await mountView()
      expect(tid(w, 'tenant-invoices').text()).toContain('No invoices yet')
    })

    it('shows a failure of the invoices inline and keeps the rest of the page', async () => {
      serve({ invoices: apiError(500, 'INTERNAL', 'Something broke.') })
      const w = await mountView()
      expect(tid(w, 'tenant-invoices').text()).toContain('Something broke.')
      expect(has(w, 'tenant-subscription')).toBe(true)
    })

    it('opens the payment dialog and the void dialog with the invoice of the row, and reads everything again when they are done', async () => {
      serve({ invoices: rows })
      const w = await mountView()
      await tid(w, 'invoice-SUB-2026-00012').get('[data-testid=invoice-pay]').trigger('click')
      expect(tid(w, 'stub-RecordPaymentDialog').attributes('data-invoice')).toBe('SUB-2026-00012')
      const before = calls('/api/v1/platform/tenants/{code}').length
      w.findComponent({ name: 'RecordPaymentDialog' }).vm.$emit('paid', {})
      await flushPromises()
      expect(has(w, 'stub-RecordPaymentDialog')).toBe(false)
      expect(calls('/api/v1/platform/tenants/{code}')).toHaveLength(before + 1)

      await tid(w, 'invoice-SUB-2026-00041').get('[data-testid=invoice-void]').trigger('click')
      expect(tid(w, 'stub-VoidInvoiceDialog').attributes('data-invoice')).toBe('SUB-2026-00041')
      w.findComponent({ name: 'VoidInvoiceDialog' }).vm.$emit('voided')
      await flushPromises()
      expect(has(w, 'stub-VoidInvoiceDialog')).toBe(false)
      expect(calls('/api/v1/platform/tenants/{code}')).toHaveLength(before + 2)
    })

    it('closes the payment dialog when it asks to', async () => {
      serve({ invoices: rows })
      const w = await mountView()
      await tid(w, 'invoice-SUB-2026-00012').get('[data-testid=invoice-pay]').trigger('click')
      w.findComponent({ name: 'RecordPaymentDialog' }).vm.$emit('update:open', false)
      await flushPromises()
      expect(has(w, 'stub-RecordPaymentDialog')).toBe(false)
    })

    it('opens the issue dialog from the card and from the head', async () => {
      const w = await mountView()
      await tid(w, 'invoices-issue').trigger('click')
      expect(tid(w, 'stub-IssueInvoiceDialog').attributes('data-code')).toBe('SGR')
      expect(tid(w, 'stub-IssueInvoiceDialog').attributes('data-plan')).toBe('PRO')
      w.findComponent({ name: 'IssueInvoiceDialog' }).vm.$emit('update:open', false)
      await flushPromises()
      expect(has(w, 'stub-IssueInvoiceDialog')).toBe(false)
      await tid(w, 'action-issue').trigger('click')
      expect(has(w, 'stub-IssueInvoiceDialog')).toBe(true)
    })
  })

  describe('suspend and reactivate', () => {
    it('opens the suspend dialog, and reads the tenant again when it is done', async () => {
      const w = await mountView()
      await tid(w, 'action-suspend').trigger('click')
      expect(tid(w, 'stub-SuspendTenantDialog').attributes('data-mode')).toBe('suspend')
      expect(tid(w, 'stub-SuspendTenantDialog').attributes('data-code')).toBe('SGR')
      const before = calls('/api/v1/platform/tenants/{code}').length
      w.findAllComponents({ name: 'SuspendTenantDialog' })[0]!.vm.$emit('done')
      await flushPromises()
      expect(calls('/api/v1/platform/tenants/{code}')).toHaveLength(before + 1)
    })

    it('opens the dialog in its reactivate mode for a suspended tenant', async () => {
      serve({ tenant: tenant({ status: 'SUSPENDED', reason: 'x' }) })
      const w = await mountView()
      await tid(w, 'action-reactivate').trigger('click')
      expect(tid(w, 'stub-SuspendTenantDialog').attributes('data-mode')).toBe('reactivate')
    })

    it('reads the tenant again after a change of the subscription', async () => {
      const w = await mountView()
      await tid(w, 'action-change').trigger('click')
      const before = calls('/api/v1/platform/tenants/{code}').length
      w.findComponent({ name: 'ChangeSubscriptionDialog' }).vm.$emit('saved')
      await flushPromises()
      expect(calls('/api/v1/platform/tenants/{code}')).toHaveLength(before + 1)
    })
  })

  describe('administrators', () => {
    it('lists name, e-mail link and last sign-in, with the note that the console shows contacts only', async () => {
      serve({
        admins: {
          items: [
            { full_name: 'Made Arsana', email: 'made@segara.example', is_active: true, last_login_at: '2026-11-13T13:40:00Z' },
            { full_name: 'Dewi Lestari', email: 'dewi@segara.example', is_active: false, last_login_at: null },
          ],
        },
      })
      const w = await mountView()
      const rows = w.findAll('[data-testid=admin-row]')
      expect(rows).toHaveLength(2)
      expect(rows[0]!.text()).toContain('Made Arsana')
      expect(rows[0]!.text()).toContain('Last sign-in')
      expect(rows[0]!.get('a').attributes('href')).toBe('mailto:made@segara.example')
      expect(rows[0]!.text()).not.toContain('Inactive')
      expect(rows[1]!.text()).toContain('Never signed in')
      expect(rows[1]!.text()).toContain('Inactive')
      expect(tid(w, 'admins-note').text()).toContain('The console shows contacts only.')
    })

    it('says when there is none, and shows a failure inline', async () => {
      const w = await mountView()
      expect(tid(w, 'admins-empty').text()).toBe('This tenant has no administrator.')
      serve({ admins: apiError(500, 'INTERNAL', 'Something broke.') })
      const failed = await mountView()
      expect(tid(failed, 'tenant-admins').text()).toContain('Something broke.')
    })
  })

  describe('activity', () => {
    const entry = (id: number, action: string, kind: 'user' | 'operator' | 'system', name?: string) => ({ id, action, entity_type: 'subscription', entity_id: 1, at: '2026-11-02T03:00:00Z', actor_kind: kind, actor_name: name })
    const five = [
      entry(1, 'subscription_invoice.issued', 'operator', 'Rina Wijaya'),
      entry(2, 'subscription.changed', 'operator', 'Rina Wijaya'),
      entry(3, 'subscription.renewed', 'user', 'Made Arsana'),
      entry(4, 'tenant.suspended', 'operator', 'Andi Saputra'),
      entry(5, 'subscription.started', 'system'),
    ]

    it('tells what was done and by whom, as the API names the actor', async () => {
      serve({ activity: { items: five } })
      const w = await mountView()
      const rows = w.findAll('[data-testid=activity-row]')
      expect(rows).toHaveLength(5)
      expect(rows[0]!.text()).toContain('Invoice issued')
      expect(rows[0]!.text()).toContain('Rina Wijaya')
      expect(rows[2]!.text()).toContain('Made Arsana')
      expect(rows[3]!.text()).toContain('Tenant suspended')
      expect(rows[4]!.text()).toContain('Subscription started')
      expect(rows[4]!.text()).toContain('system')
    })

    it('never shows the code of an action it has no words for', async () => {
      serve({ activity: { items: [entry(1, 'subscription.something_new', 'system')] } })
      const w = await mountView()
      const text = tid(w, 'activity-row').text()
      expect(text).toContain('Change of the subscription')
      expect(text).not.toContain('something_new')
    })

    it('loads 200 with "See all", once', async () => {
      serve({ activity: { items: five } })
      const w = await mountView()
      await tid(w, 'activity-all').trigger('click')
      await flushPromises()
      expect(GET).toHaveBeenLastCalledWith('/api/v1/platform/tenants/{code}/activity', { params: { path: { code: 'SGR' }, query: { limit: 200 } } })
      expect(has(w, 'activity-all')).toBe(false)
    })

    it('has no "See all" when everything is shown, and says when there is nothing', async () => {
      serve({ activity: { items: five.slice(0, 2) } })
      const w = await mountView()
      expect(has(w, 'activity-all')).toBe(false)
      serve()
      const empty = await mountView()
      expect(tid(empty, 'activity-empty').text()).toBe('Nothing yet.')
    })

    it('shows a failure inline', async () => {
      serve({ activity: apiError(500, 'INTERNAL', 'Something broke.') })
      const w = await mountView()
      expect(tid(w, 'tenant-activity').text()).toContain('Something broke.')
    })
  })

  describe('another language', () => {
    it('shows the Indonesian words of the design', async () => {
      await setLocale('id')
      serve({ tenant: graceTenant(), invoices: { items: [invoice({ is_overdue: true })] } })
      const w = await mountView()
      expect(tid(w, 'tenant-breadcrumb').text()).toBe('Penyewa / SGR')
      expect(tid(w, 'tenant-callout').text()).toContain('Masa tenggang berakhir')
      expect(tid(w, 'callout-pay').text()).toBe('Catat pembayaran')
      expect(tid(w, 'action-issue').text()).toBe('Terbitkan invoice')
      expect(tid(w, 'invoice-SUB-2026-00041').text()).toContain('Lewat jatuh tempo')
      expect(tid(w, 'admins-empty').text()).toContain('belum punya admin')
    })
  })
})
