import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { setLocale } from '@/i18n'
import InvoicesView from './InvoicesView.vue'

let GET = vi.fn()
let POST = vi.fn()
vi.mock('@/api/platformClient', () => ({ platformApi: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a) } }))

const inv = (over: object = {}) => ({
  id: 1, number: 'SUB-2026-00033', plan_id: 1, plan_code: 'BASIC', period_start: '2026-10-16', period_end: '2026-11-15', amount: '1500000', currency_code: 'IDR', status: 'ISSUED',
  issued_at: '2026-10-01T00:00:00Z', due_on: '2026-10-16', is_overdue: true, tenant_code: 'TMR', tenant_name: 'Taman Ria Hostel', overdue_days: 29, ...over,
})
const open = [
  inv(),
  inv({ id: 2, number: 'SUB-2026-00041', plan_code: 'PRO', tenant_code: 'SGR', tenant_name: 'Segara Resort Group', amount: '7500000', overdue_days: 1, due_on: '2026-11-13' }),
  inv({ id: 3, number: 'SUB-2026-00056', tenant_code: 'LBK', tenant_name: 'Lembah Kopi Inn', is_overdue: false, overdue_days: 0, due_on: '2026-11-29' }),
]
const paid = Array.from({ length: 41 }, (_, i) => inv({ id: 100 + i, number: `SUB-2026-${String(i).padStart(5, '0')}`, status: 'PAID', paid_on: '2026-10-01', payment_reference: 'TRF-1', is_overdue: false, overdue_days: 0 }))
const voided = [inv({ id: 200, number: 'SUB-2026-00099', status: 'VOID', void_reason: 'Issued twice', is_overdue: false, overdue_days: 0 })]
const total = [{ currency_code: 'IDR', invoice_count: 3, total: '10500000' }]

function serve(lists: Partial<Record<'open' | 'overdue' | 'paid' | 'void', object[]>> = {}) {
  GET = vi.fn(async (_path: string, opts: { params: { query: { status: string } } }) => {
    const status = opts.params.query.status as 'open' | 'overdue' | 'paid' | 'void'
    const defaults = { open, overdue: open.slice(0, 2), paid, void: voided }
    return { data: { items: lists[status] ?? defaults[status], total_open: total } }
  })
}

async function mountPage() {
  setActivePinia(createPinia())
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  await router.push('/platform/invoices')
  const wrapper = mount(InvoicesView, { global: { plugins: [router] }, attachTo: document.body })
  await flushPromises()
  return wrapper
}

describe('InvoicesView', () => {
  beforeEach(async () => {
    GET = vi.fn()
    POST = vi.fn()
    await setLocale('en')
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(new Date('2026-11-14T08:00:00Z'))
  })
  afterEach(() => {
    document.body.innerHTML = ''
    vi.useRealTimers()
  })

  it('shows a skeleton while loading', async () => {
    GET = vi.fn(() => new Promise(() => undefined))
    setActivePinia(createPinia())
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
    await router.push('/platform/invoices')
    const wrapper = mount(InvoicesView, { global: { plugins: [router] } })
    await flushPromises()
    expect(wrapper.find('[data-slot=skeleton]').exists()).toBe(true)
  })

  it('has one h1 and the four tabs with their counts, Open first', async () => {
    serve()
    const wrapper = await mountPage()
    expect(wrapper.findAll('h1')).toHaveLength(1)
    expect(wrapper.get('[data-testid=count-open]').text()).toBe('3')
    expect(wrapper.get('[data-testid=count-overdue]').text()).toBe('2')
    expect(wrapper.get('[data-testid=count-paid]').text()).toBe('41')
    expect(wrapper.get('[data-testid=count-void]').text()).toBe('1')
    expect(wrapper.get('[data-testid=tab-open]').attributes('aria-selected')).toBe('true')
    expect(GET).toHaveBeenCalledTimes(4)
  })

  it('lists the open invoices with tenant link, period, amount, lateness and the footer total', async () => {
    serve()
    const wrapper = await mountPage()
    const r = (n: string) => wrapper.get(`[data-testid=invoice-${n}]`)
    expect(r('SUB-2026-00033').text()).toContain('Taman Ria Hostel')
    expect(r('SUB-2026-00033').text()).toContain('BASIC')
    expect(r('SUB-2026-00033').text()).toContain('IDR')
    expect(r('SUB-2026-00033').get('[data-testid=tenant-link]').attributes('href')).toBe('/platform/tenants/TMR')
    expect(r('SUB-2026-00033').get('[data-testid=due-hint]').text()).toBe('29 days late')
    expect(r('SUB-2026-00033').get('[data-testid=due-hint]').classes()).toContain('text-destructive')
    expect(r('SUB-2026-00041').get('[data-testid=due-hint]').text()).toBe('1 day late')
    expect(r('SUB-2026-00056').get('[data-testid=due-hint]').text()).toBe('in 15 days')
    expect(r('SUB-2026-00056').get('[data-testid=due-hint]').classes()).not.toContain('text-destructive')
    expect(wrapper.get('[data-testid=invoice-total]').text()).toContain('Total open')
    expect(wrapper.get('[data-testid=total-IDR]').text()).toContain('IDR')
    expect(wrapper.text()).toContain('3 open invoices')
  })

  it('switches tabs; paid and voided invoices have no actions and say when/why', async () => {
    serve()
    const wrapper = await mountPage()
    await wrapper.get('[data-testid=tab-overdue]').trigger('mousedown')
    await wrapper.get('[data-testid=tab-overdue]').trigger('click')
    await flushPromises()
    expect(wrapper.findAll('[data-testid^=invoice-SUB]')).toHaveLength(2)
    expect(wrapper.find('[data-testid=invoice-total]').exists()).toBe(false)

    await wrapper.get('[data-testid=tab-void]').trigger('mousedown')
    await flushPromises()
    expect(wrapper.text()).toContain('reason: Issued twice')
    expect(wrapper.find('[data-testid=pay-invoice]').exists()).toBe(false)
    expect(wrapper.find('[data-testid=void-invoice]').exists()).toBe(false)

    await wrapper.get('[data-testid=tab-paid]').trigger('mousedown')
    await flushPromises()
    expect(wrapper.text()).toContain('paid 1 Oct 2026')
    expect(wrapper.find('[data-testid=pay-invoice]').exists()).toBe(false)
    expect(wrapper.text()).toContain('41 paid invoices')
  })

  it('says when a tab is empty', async () => {
    serve({ open: [], overdue: [] })
    const wrapper = await mountPage()
    expect(wrapper.text()).toContain('No open invoices')
    expect(wrapper.get('[data-testid=count-open]').text()).toBe('0')
    expect(wrapper.find('[data-testid=invoice-total]').exists()).toBe(true)
  })

  it('shows the error inline when a list cannot be read', async () => {
    GET = vi.fn(async () => { throw new ApiError({ status: 500, code: 'INTERNAL', title: 'Boom', type: 'about:blank' } as never) })
    const wrapper = await mountPage()
    expect(wrapper.find('[data-testid=invoices-error]').exists()).toBe(true)
  })

  it('records a payment: opens the dialog for that invoice, and reads the lists again after it', async () => {
    serve()
    POST = vi.fn(async () => ({ data: { invoice: inv({ status: 'PAID' }), subscription: {} } }))
    const wrapper = await mountPage()
    await wrapper.get('[data-testid=invoice-SUB-2026-00041] [data-testid=pay-invoice]').trigger('click')
    await flushPromises()
    expect(document.body.querySelector('[data-testid=record-payment-dialog]')!.textContent).toContain('SUB-2026-00041')
    document.body.querySelector<HTMLFormElement>('[data-testid=pay-form]')!.dispatchEvent(new Event('submit', { cancelable: true }))
    await flushPromises()
    expect(POST.mock.calls[0]![0]).toBe('/api/v1/platform/invoices/{number}/pay')
    expect(POST.mock.calls[0]![1].params).toEqual({ path: { number: 'SUB-2026-00041' } })
    expect(GET).toHaveBeenCalledTimes(8)
    expect(document.body.querySelector('[data-testid=record-payment-dialog]')).toBeNull()
  })

  it('voids an invoice with a reason, and reads the lists again', async () => {
    serve()
    POST = vi.fn(async () => ({ data: {} }))
    const wrapper = await mountPage()
    await wrapper.get('[data-testid=invoice-SUB-2026-00056] [data-testid=void-invoice]').trigger('click')
    await flushPromises()
    const reason = document.body.querySelector<HTMLInputElement>('input[name=reason]')!
    reason.value = 'Wrong plan'
    reason.dispatchEvent(new Event('input'))
    await flushPromises()
    document.body.querySelector<HTMLFormElement>('[data-testid=void-form]')!.dispatchEvent(new Event('submit', { cancelable: true }))
    await flushPromises()
    expect(POST).toHaveBeenCalledWith('/api/v1/platform/invoices/{number}/void', { params: { path: { number: 'SUB-2026-00056' } }, body: { reason: 'Wrong plan' } })
    expect(GET).toHaveBeenCalledTimes(8)
  })
})
