import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { setLocale } from '@/i18n'
import SubscriptionView from './SubscriptionView.vue'

let GET = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...args: unknown[]) => GET(...args) } }))

const summary = (over: object = {}) => ({
  plan_code: 'STD', plan_name: 'Standard', modules: ['CORE', 'POS'], status: 'ACTIVE', access: 'FULL', started_on: '2026-01-01', paid_through: '2026-12-31', grace_ends_on: '2027-01-14', days_left: 40,
  limits: { max_properties: 2, max_rooms: 100, max_users: null }, usage: { properties: 1, rooms: 90, users: 7 }, is_trial_ending: false, billing_contact: null, ...over,
})
const invoice = (over: object = {}) => ({
  id: 1, number: 'SUB-2026-00001', plan_id: 1, plan_code: 'STD', period_start: '2026-01-01', period_end: '2026-12-31', amount: '1200000', currency_code: 'IDR', status: 'ISSUED', issued_at: '2026-01-01T00:00:00Z', due_on: '2026-01-15', is_overdue: false, ...over,
})

function serve(sum: object | Error, invoices: object[] | Error = []) {
  GET = vi.fn(async (path: string) => {
    const r = path === '/api/v1/subscription' ? sum : invoices
    if (r instanceof Error) throw r
    return { data: path === '/api/v1/subscription' ? r : { items: r } }
  })
}

async function mountPage() {
  setActivePinia(createPinia())
  const wrapper = mount(SubscriptionView, { global: { plugins: [] } })
  await flushPromises()
  return wrapper
}

describe('SubscriptionView', () => {
  beforeEach(() => setLocale('en'))

  it('shows a skeleton while loading, then the plan', async () => {
    serve(summary())
    setActivePinia(createPinia())
    const wrapper = mount(SubscriptionView)
    expect(wrapper.find('[data-testid=subscription-loading]').exists()).toBe(true)
    await flushPromises()
    expect(wrapper.find('[data-testid=subscription-loading]').exists()).toBe(false)
    expect(wrapper.get('h1').text()).toBe('Subscription')
    expect(wrapper.get('#sub-plan-title').text()).toBe('Plan Standard')
    expect(wrapper.get('[data-testid=access-badge]').text()).toBe('Active')
    expect(wrapper.get('[data-testid=days-left]').text()).toBe('40')
  })

  it('names each access state in words', async () => {
    serve(summary({ status: 'TRIAL' }))
    expect((await mountPage()).get('[data-testid=access-badge]').text()).toBe('Trial')
    serve(summary({ access: 'GRACE', days_left: 9 }))
    const grace = await mountPage()
    expect(grace.get('[data-testid=access-badge]').text()).toBe('Grace period')
    expect(grace.find('[data-testid=grace-until]').exists()).toBe(true)
    serve(summary({ access: 'READ_ONLY', days_left: 0 }))
    expect((await mountPage()).get('[data-testid=access-badge]').text()).toBe('Read only')
  })

  it('has no days and no end when the subscription never ends', async () => {
    serve(summary({ paid_through: null, grace_ends_on: null, days_left: null }))
    const wrapper = await mountPage()
    expect(wrapper.get('[data-testid=paid-through]').text()).toBe('No end date')
    expect(wrapper.find('[data-testid=days-left]').exists()).toBe(false)
    expect(wrapper.find('[data-testid=grace-until]').exists()).toBe(false)
  })

  it('reads the meters in numbers, in words and for assistive technology', async () => {
    serve(summary({ usage: { properties: 2, rooms: 90, users: 7 } }))
    const wrapper = await mountPage()
    const rooms = wrapper.get('[data-testid=quota-rooms]')
    expect(rooms.text()).toContain('90 of 100')
    expect(rooms.attributes('data-level')).toBe('near')
    expect(rooms.get('[data-testid=quota-note]').text()).toBe('Almost full: 10 left.')
    const meter = rooms.get('[role=meter]')
    expect(meter.attributes('aria-valuenow')).toBe('90')
    expect(meter.attributes('aria-valuemax')).toBe('100')
    const properties = wrapper.get('[data-testid=quota-properties]')
    expect(properties.attributes('data-level')).toBe('full')
    expect(properties.get('[data-testid=quota-note]').text()).toBe('Full: a new property cannot be added.')
    const users = wrapper.get('[data-testid=quota-users]')
    expect(users.text()).toContain('Unlimited')
    expect(users.find('[role=meter]').exists()).toBe(false)
    expect(users.find('[data-testid=quota-note]').exists()).toBe(false)
  })

  it('lists the modules of the plan', async () => {
    serve(summary())
    const wrapper = await mountPage()
    expect(wrapper.get('[data-testid=module-CORE]').text()).toContain('Always included')
    expect(wrapper.get('[data-testid=module-POS]').text()).toContain('Included')
    expect(wrapper.get('[data-testid=module-POS]').text()).not.toContain('Not included')
    expect(wrapper.get('[data-testid=module-BANQUET]').text()).toContain('Not included')
  })

  it('shows the billing contact only when there is one', async () => {
    serve(summary({ billing_contact: 'billing@example.com · +62 811' }))
    expect((await mountPage()).get('[data-testid=billing-contact]').text()).toContain('billing@example.com · +62 811')
    serve(summary())
    expect((await mountPage()).find('[data-testid=billing-contact]').exists()).toBe(false)
  })

  it('lists the invoices with their status, and an overdue one as unpaid', async () => {
    serve(summary(), [
      invoice({ id: 3, number: 'SUB-2026-00003', is_overdue: true }),
      invoice({ id: 2, number: 'SUB-2026-00002' }),
      invoice({ id: 1, number: 'SUB-2025-00001', status: 'PAID' }),
      invoice({ id: 0, number: 'SUB-2025-00000', status: 'VOID' }),
    ])
    const text = (await mountPage()).get('[data-testid=subscription-invoices]').text()
    expect(text).toContain('SUB-2026-00003')
    for (const word of ['Unpaid', 'Issued', 'Paid', 'Voided']) expect(text).toContain(word)
    expect(text).toContain('IDR 1.200.000'.replace(/\./g, text.includes('1,200,000') ? ',' : '.'))
  })

  it('says so when there are no invoices, and has no actions', async () => {
    serve(summary(), [])
    const wrapper = await mountPage()
    expect(wrapper.get('[data-testid=subscription-invoices]').text()).toContain('No invoices yet')
    expect(wrapper.findAll('button')).toHaveLength(0)
  })

  it('shows an error when the subscription cannot be read', async () => {
    serve(new ApiError({ type: 't', title: 'T', status: 500, code: 'INTERNAL', detail: 'boom', request_id: 'r' }))
    const wrapper = await mountPage()
    expect(wrapper.find('[data-testid=subscription-plan]').exists()).toBe(false)
    expect(wrapper.find('[data-testid=subscription-loading]').exists()).toBe(false)
  })

  it('shows the invoice error inside its card and keeps the rest', async () => {
    serve(summary(), new ApiError({ type: 't', title: 'T', status: 403, code: 'PERMISSION_DENIED', detail: 'no', request_id: 'r' }))
    const wrapper = await mountPage()
    expect(wrapper.find('[data-testid=subscription-plan]').exists()).toBe(true)
    expect(wrapper.get('[data-testid=subscription-invoices]').find('[role=alert], .alert').exists()).toBe(true)
  })

  it('speaks Indonesian', async () => {
    setLocale('id')
    serve(summary({ access: 'GRACE', days_left: 9 }))
    const wrapper = await mountPage()
    expect(wrapper.get('h1').text()).toBe('Langganan')
    expect(wrapper.get('[data-testid=access-badge]').text()).toBe('Masa tenggang')
  })
})
