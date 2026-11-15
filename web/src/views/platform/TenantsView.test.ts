import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { setLocale } from '@/i18n'
import TenantsView from './TenantsView.vue'

let GET = vi.fn()
vi.mock('@/api/platformClient', () => ({ platformApi: { GET: (...a: unknown[]) => GET(...a), POST: vi.fn() } }))
let platformFetch = vi.fn()
vi.mock('@/api/platformSession', () => ({ platformFetch: (...a: unknown[]) => platformFetch(...a) }))

const row = (over: object = {}) => ({
  code: 'SGR', name: 'Segara Resort Group', timezone: 'Asia/Jakarta', state: 'active', tenant_status: 'ACTIVE', status_reason: null, plan_code: 'PRO', plan_name: 'Pro', subscription_status: 'ACTIVE',
  access: 'FULL', paid_through: '2026-12-31', days_left: 47, trial_ending: false, usage: { properties: 1, rooms: 86, users: 18 }, limits: { max_properties: 3, max_rooms: 120, max_users: 30 },
  open_invoice_count: 0, overdue_invoice_count: 0, created_at: '2026-01-01T00:00:00Z', ...over,
})
const summary = (over: object = {}) => ({
  active: 3, trial: 1, trial_ending: 1, grace: 1, read_only: 1, suspended: 1, open_invoice_count: 3, overdue_invoice_count: 2,
  open_invoice_totals: [{ currency_code: 'IDR', invoice_count: 3, total: '10500000' }], ...over,
})
const rows = [
  row({ code: 'TMR', name: 'Taman Ria Hostel', plan_code: 'BASIC', state: 'read_only', access: 'READ_ONLY', paid_through: '2026-10-15', days_left: null, open_invoice_count: 1, overdue_invoice_count: 1, usage: { properties: 1, rooms: 30, users: 5 }, limits: { max_properties: 1, max_rooms: 30, max_users: 5 } }),
  row({ code: 'SGR', state: 'grace', access: 'GRACE', days_left: 8, open_invoice_count: 2, overdue_invoice_count: 1 }),
  row({ code: 'LBK', name: 'Lembah Kopi Inn', plan_code: 'BASIC', state: 'trial', subscription_status: 'TRIAL', days_left: 14, trial_ending: true }),
  row({ code: 'KRN', name: 'Kirana Hotels' }),
  row({ code: 'NSA', name: 'Nusa Asri Hotels', plan_code: 'ENTERPRISE', paid_through: null, days_left: null, limits: { max_properties: null, max_rooms: null, max_users: null }, usage: { properties: 3, rooms: 410, users: 64 } }),
  row({ code: 'BKT', name: 'Bukit Hijau Lodge', state: 'suspended', tenant_status: 'SUSPENDED', access: 'READ_ONLY', paid_through: '2026-09-30', days_left: null }),
]

function serve(list: object[] = rows, sum: object = summary()) {
  GET = vi.fn(async (path: string) => (path === '/api/v1/platform/plans' ? { data: { items: [{ code: 'BASIC' }, { code: 'PRO' }, { code: 'ENTERPRISE' }] } } : { data: { items: list, summary: sum } }))
}

async function mountPage(url = '/platform/tenants') {
  setActivePinia(createPinia())
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  await router.push(url)
  const wrapper = mount(TenantsView, { global: { plugins: [router] }, attachTo: document.body })
  await flushPromises()
  return { wrapper, router }
}
const tenantCalls = () => GET.mock.calls.filter((c) => c[0] === '/api/v1/platform/tenants')

describe('TenantsView', () => {
  beforeEach(async () => {
    GET = vi.fn()
    platformFetch = vi.fn()
    await setLocale('en')
  })
  afterEach(() => {
    document.body.innerHTML = ''
  })

  it('shows a skeleton while loading', async () => {
    GET = vi.fn(() => new Promise(() => undefined))
    setActivePinia(createPinia())
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
    await router.push('/platform/tenants')
    const wrapper = mount(TenantsView, { global: { plugins: [router] } })
    await flushPromises()
    expect(wrapper.find('[data-testid=tenants-kpis]').exists()).toBe(false)
    expect(wrapper.find('[data-slot=skeleton]').exists()).toBe(true)
  })

  it('has one h1 and the KPI row from the summary, with the amounts per currency', async () => {
    serve(rows, summary({ open_invoice_totals: [{ currency_code: 'IDR', invoice_count: 2, total: '9000000' }, { currency_code: 'USD', invoice_count: 1, total: '120' }] }))
    const { wrapper } = await mountPage()
    expect(wrapper.findAll('h1')).toHaveLength(1)
    const kpi = (k: string) => wrapper.get(`[data-testid=kpi-${k}]`).text()
    expect(kpi('active')).toContain('3')
    expect(kpi('trial')).toContain('1 ending within 14 days')
    expect(kpi('grace')).toContain('1')
    expect(kpi('read_only')).toContain('1')
    expect(kpi('open')).toContain('IDR')
    expect(kpi('open')).toContain('USD')
    expect(kpi('open')).toContain('3 invoices, 2 overdue')
  })

  it('shows a dash and "None open" when nothing is owed', async () => {
    serve(rows, summary({ open_invoice_count: 0, overdue_invoice_count: 0, open_invoice_totals: [] }))
    const { wrapper } = await mountPage()
    expect(wrapper.get('[data-testid=kpi-open]').text()).toContain('None open')
  })

  it('lists the tenants with name, code, plan, state words, hints, rooms, users and open invoices', async () => {
    serve()
    const { wrapper } = await mountPage()
    const r = (code: string) => wrapper.get(`[data-testid=tenant-${code}]`)
    expect(r('TMR').text()).toContain('Taman Ria Hostel')
    expect(r('TMR').text()).toContain('BASIC')
    expect(r('TMR').text()).toContain('Read only')
    expect(r('TMR').find('[data-testid=open-cell]').text()).toContain('1 overdue')
    expect(r('TMR').get('[data-testid=open-cell]').classes()).toContain('text-destructive')
    expect(r('TMR').get('[data-testid=rooms-cell]').text()).toBe('30 / 30')
    expect(r('TMR').find('[data-testid=rooms-cell] .bg-destructive').exists()).toBe(true)
    expect(r('SGR').get('[data-testid=paid-hint]').text()).toBe('grace: 8 days left')
    expect(r('SGR').text()).toContain('Grace period')
    expect(r('LBK').get('[data-testid=paid-hint]').text()).toBe('trial: 14 days left')
    expect(r('LBK').text()).toContain('Trial')
    expect(r('KRN').get('[data-testid=paid-hint]').text()).toBe('47 days left')
    expect(r('KRN').text()).toContain('Active')
    expect(r('KRN').get('[data-testid=users-cell]').text()).toBe('18 / 30')
    expect(r('KRN').text()).toContain('–')
    expect(r('NSA').text()).toContain('No end date')
    expect(r('NSA').get('[data-testid=users-cell]').text()).toBe('64 / ∞')
    expect(r('NSA').get('[data-testid=rooms-cell]').text()).toBe('410 / ∞')
    // the track of the bar is the border colour, which can be seen on white
    expect(r('KRN').find('[data-testid=rooms-cell] .bg-border').exists()).toBe(true)
    expect(r('BKT').text()).toContain('Suspended')
    expect(r('BKT').get('[data-testid=paid-hint]').text()).toBe('sign-in blocked')
    expect(wrapper.text()).toContain('6 tenants')
  })

  it('links the name and "Open" to the tenant', async () => {
    serve()
    const { wrapper } = await mountPage()
    const tr = wrapper.get('[data-testid=tenant-SGR]')
    expect(tr.get('[data-testid=tenant-link]').attributes('href')).toBe('/platform/tenants/SGR')
    expect(tr.get('[data-testid=open-tenant]').attributes('href')).toBe('/platform/tenants/SGR')
  })

  it('reads the filters from the address and asks the server with them', async () => {
    serve([rows[0]!])
    const { wrapper } = await mountPage('/platform/tenants?q=taman&status=read_only&plan=BASIC')
    expect(tenantCalls()[0]![1]).toEqual({ params: { query: { q: 'taman', status: 'read_only', plan: 'BASIC' } } })
    expect((wrapper.get('input[name=q]').element as HTMLInputElement).value).toBe('taman')
    expect((wrapper.get('select[name=status]').element as HTMLSelectElement).value).toBe('read_only')
    expect((wrapper.get('select[name=plan]').element as HTMLSelectElement).value).toBe('BASIC')
  })

  it('ignores a status it does not know', async () => {
    serve()
    await mountPage('/platform/tenants?status=bogus')
    expect(tenantCalls()[0]![1]).toEqual({ params: { query: {} } })
  })

  it('writes a changed status and plan to the address and loads again', async () => {
    serve()
    const { wrapper, router } = await mountPage()
    await wrapper.get('select[name=status]').setValue('grace')
    await flushPromises()
    expect(router.currentRoute.value.query).toEqual({ status: 'grace' })
    await wrapper.get('select[name=plan]').setValue('PRO')
    await flushPromises()
    expect(router.currentRoute.value.query).toEqual({ status: 'grace', plan: 'PRO' })
    expect(tenantCalls().at(-1)![1]).toEqual({ params: { query: { status: 'grace', plan: 'PRO' } } })
  })

  it('searches when the form is submitted', async () => {
    serve()
    const { wrapper, router } = await mountPage()
    await wrapper.get('input[name=q]').setValue('  kopi ')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(router.currentRoute.value.query).toEqual({ q: 'kopi' })
    expect(tenantCalls().at(-1)![1]).toEqual({ params: { query: { q: 'kopi' } } })
  })

  it('follows a link from the plans screen (the address changes while the page is open)', async () => {
    serve()
    const { router } = await mountPage()
    await router.push('/platform/tenants?plan=PRO')
    await flushPromises()
    expect(tenantCalls().at(-1)![1]).toEqual({ params: { query: { plan: 'PRO' } } })
  })

  it('says so when nothing matches the filters, and when there are no tenants at all', async () => {
    serve([], summary({ active: 0, trial: 0, grace: 0, read_only: 0, open_invoice_count: 0, open_invoice_totals: [] }))
    const filtered = await mountPage('/platform/tenants?q=zzz')
    expect(filtered.wrapper.text()).toContain('No tenant matches these filters.')
    document.body.innerHTML = ''
    const all = await mountPage()
    expect(all.wrapper.text()).toContain('No tenants')
  })

  it('shows the error inline when the list cannot be read', async () => {
    GET = vi.fn(async (path: string) => {
      if (path === '/api/v1/platform/plans') return { data: { items: [] } }
      throw new ApiError({ status: 500, code: 'INTERNAL', title: 'Boom', type: 'about:blank' } as never)
    })
    const { wrapper } = await mountPage()
    expect(wrapper.find('[data-testid=tenants-error]').exists()).toBe(true)
    expect(wrapper.find('[data-testid=tenants-kpis]').exists()).toBe(false)
  })

  it('exports the CSV through the console client with the current filters', async () => {
    serve()
    platformFetch = vi.fn(async () => new Response('code,name\r\nSGR,Segara\r\n', { status: 200, headers: { 'Content-Disposition': 'attachment; filename="tenants.csv"' } }))
    const createObjectURL = vi.fn(() => 'blob:x')
    Object.assign(URL, { createObjectURL, revokeObjectURL: vi.fn() })
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined)
    const { wrapper } = await mountPage('/platform/tenants?status=grace&plan=PRO')
    await wrapper.get('[data-testid=export-csv]').trigger('click')
    await flushPromises()
    expect(platformFetch).toHaveBeenCalledTimes(1)
    const req = platformFetch.mock.calls[0]![0] as Request
    const url = new URL(req.url)
    expect(url.pathname).toBe('/api/v1/platform/tenants/export.csv')
    expect(url.searchParams.get('status')).toBe('grace')
    expect(url.searchParams.get('plan')).toBe('PRO')
    expect(url.searchParams.get('lang')).toBe('en')
    expect(createObjectURL).toHaveBeenCalledTimes(1)
    expect(click).toHaveBeenCalledTimes(1)
    click.mockRestore()
  })

  it('does not save a file when the export is refused', async () => {
    serve()
    platformFetch = vi.fn(async () => new Response(JSON.stringify({ status: 403, code: 'PERMISSION_DENIED', title: 'no', type: 'about:blank' }), { status: 403 }))
    const createObjectURL = vi.fn(() => 'blob:x')
    Object.assign(URL, { createObjectURL })
    const { wrapper } = await mountPage()
    await wrapper.get('[data-testid=export-csv]').trigger('click')
    await flushPromises()
    expect(createObjectURL).not.toHaveBeenCalled()
  })

  it('opens the new-tenant dialog and goes to the tenant it created', async () => {
    serve()
    const { wrapper, router } = await mountPage()
    await wrapper.get('[data-testid=new-tenant]').trigger('click')
    await flushPromises()
    expect(document.body.querySelector('[data-testid=new-tenant-dialog]')).not.toBeNull()
    wrapper.getComponent({ name: 'NewTenantDialog' }).vm.$emit('created', 'NEW1')
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/platform/tenants/NEW1')
  })
})
