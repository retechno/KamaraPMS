import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { setLocale } from '@/i18n'
import { plans } from '@/views/platform/tenantFixture'
import IssueInvoiceDialog from './IssueInvoiceDialog.vue'

let GET = vi.fn()
let POST = vi.fn()
vi.mock('@/api/platformClient', () => ({ platformApi: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a) } }))

const q = <T extends Element>(sel: string) => document.body.querySelector<T>(sel)!
let wrapper: VueWrapper | null = null

const PRICE: Record<string, number> = { PRO: 7500000, BASIC: 2500000 }
function previewFor(args: { params: { query: { plan: string; periods: number } } }, withOpen: boolean | 'overlap' = false) {
  const { plan, periods } = args.params.query
  return {
    data: {
      plan_code: plan,
      period_start: '2027-02-09',
      period_end: '2027-05-08',
      amount: `${PRICE[plan]! * periods}.000`,
      currency_code: 'IDR',
      due_on: '2027-02-09',
      next_number: 'SUB-2026-00058',
      ...(withOpen === 'overlap' ? { open_invoice: { number: 'SUB-2026-00041', period_start: '2027-01-01', period_end: '2027-04-01' } } : withOpen ? { open_invoice: { number: 'SUB-2026-00041', period_start: '2026-11-09', period_end: '2027-02-08' } } : {}),
    },
  }
}

async function open() {
  wrapper = mount(IssueInvoiceDialog, { props: { open: true, tenantCode: 'SGR', currentPlan: 'PRO', plans }, attachTo: document.body })
  await flushPromises()
  return wrapper
}
async function type(sel: string, value: string) {
  const el = q<HTMLInputElement>(sel)
  el.value = value
  el.dispatchEvent(new Event('input', { bubbles: true }))
  await flushPromises()
}
async function choose(sel: string, value: string) {
  const el = q<HTMLSelectElement>(sel)
  el.value = value
  el.dispatchEvent(new Event('change', { bubbles: true }))
  await flushPromises()
}
const submit = async () => {
  q<HTMLButtonElement>('[data-testid=dialog-submit]').click()
  await flushPromises()
}

describe('IssueInvoiceDialog', () => {
  beforeEach(async () => {
    GET = vi.fn(async (_url: string, args: never) => previewFor(args))
    POST = vi.fn(async () => ({ data: {} }))
    await setLocale('en')
  })
  afterEach(() => {
    wrapper?.unmount()
    wrapper = null
    document.body.innerHTML = ''
  })

  it('asks the server for the preview of the current plan and one period, and shows it', async () => {
    await open()
    expect(GET).toHaveBeenCalledWith('/api/v1/platform/tenants/{code}/invoices/preview', { params: { path: { code: 'SGR' }, query: { plan: 'PRO', periods: 1 } } })
    expect(q('[data-testid=preview-period]').textContent).toBe('9 Feb 2027 – 8 May 2027')
    expect(q('[data-testid=preview-number]').textContent).toBe('SUB-2026-00058')
    expect(q<HTMLInputElement>('input[name=amount]').value).toBe('7,500,000')
    expect(q<HTMLInputElement>('input[type=date][name=due_on]').value).toBe('2027-02-09')
    expect(document.body.querySelector('[data-testid=open-invoice-conflict]')).toBeNull()
  })

  it('lists the plans that are for sale with their price and period, and puts the cursor in the plan', async () => {
    await open()
    expect([...document.body.querySelectorAll('select[name=plan_code] option')].map((o) => o.textContent)).toEqual(['PRO · IDR 7,500,000 / 3 months', 'BASIC · IDR 2,500,000 / 3 months'])
    expect(document.activeElement).toBe(q('select[name=plan_code]'))
  })

  it('says only that an invoice is still open when its period does not share a day with the new one, and lets the invoice be issued', async () => {
    GET = vi.fn(async (_url: string, args: never) => previewFor(args, true))
    await open()
    expect(q('[data-testid=open-invoice-conflict]').textContent?.trim()).toBe('SUB-2026-00041 is still open for 9 Nov 2026 – 8 Feb 2027.')
    expect(document.body.querySelector('[data-testid=open-invoice-overlap]')).toBeNull()
    expect(q<HTMLButtonElement>('[data-testid=dialog-submit]').disabled).toBe(false)
  })

  it('warns in the danger colour that the invoice will be refused when the periods share a day, and turns Issue off', async () => {
    GET = vi.fn(async (_url: string, args: never) => previewFor(args, 'overlap'))
    await open()
    const alert = q('[data-testid=open-invoice-overlap]')
    expect(alert.getAttribute('role')).toBe('alert')
    expect(alert.classList.contains('text-destructive')).toBe(true)
    expect(alert.textContent).toContain('INVOICE_PERIOD_OVERLAP')
    expect(alert.textContent).toContain('Record the payment or void SUB-2026-00041 first.')
    expect(document.body.querySelector('[data-testid=open-invoice-conflict]')).toBeNull()
    expect(q<HTMLButtonElement>('[data-testid=dialog-submit]').disabled).toBe(true)
    await submit()
    expect(POST).not.toHaveBeenCalled()
  })

  it('says the same in Indonesian', async () => {
    await setLocale('id')
    GET = vi.fn(async (_url: string, args: never) => previewFor(args, 'overlap'))
    await open()
    expect(q('[data-testid=open-invoice-overlap]').textContent).toContain('Invoice ini akan ditolak (INVOICE_PERIOD_OVERLAP)')
    expect(q('[data-testid=open-invoice-overlap]').textContent).toContain('Catat pembayaran atau void SUB-2026-00041 dulu.')
    wrapper?.unmount()
    document.body.innerHTML = ''
    GET = vi.fn(async (_url: string, args: never) => previewFor(args, true))
    await open()
    expect(q('[data-testid=open-invoice-conflict]').textContent?.trim()).toBe('SUB-2026-00041 masih terbuka untuk 9 Nov 2026 – 8 Feb 2027.')
    await setLocale('en')
  })

  it('works out the amount again when the periods or the plan change', async () => {
    await open()
    await type('input[name=periods]', '2')
    expect(GET).toHaveBeenLastCalledWith('/api/v1/platform/tenants/{code}/invoices/preview', { params: { path: { code: 'SGR' }, query: { plan: 'PRO', periods: 2 } } })
    expect(q<HTMLInputElement>('input[name=amount]').value).toBe('15,000,000')
    await choose('select[name=plan_code]', 'BASIC')
    expect(GET).toHaveBeenLastCalledWith('/api/v1/platform/tenants/{code}/invoices/preview', { params: { path: { code: 'SGR' }, query: { plan: 'BASIC', periods: 2 } } })
    expect(q<HTMLInputElement>('input[name=amount]').value).toBe('5,000,000')
  })

  it('keeps an amount the operator typed (a discount) when the periods change', async () => {
    await open()
    await type('input[name=amount]', '6,000,000')
    await type('input[name=periods]', '2')
    expect(q<HTMLInputElement>('input[name=amount]').value).toBe('6,000,000')
  })

  it('does not ask for a preview of periods outside 1 to 36, and does not send them', async () => {
    await open()
    GET.mockClear()
    await type('input[name=periods]', '40')
    expect(GET).not.toHaveBeenCalled()
    await submit()
    expect(POST).not.toHaveBeenCalled()
    expect(q('[data-slot=form-field] [role=alert]').textContent).toBe('A whole number from 1 to 36.')
  })

  it('wants an amount', async () => {
    await open()
    await type('input[name=amount]', '')
    await submit()
    expect(POST).not.toHaveBeenCalled()
    expect(document.body.textContent).toContain('An amount of zero or more.')
  })

  it('issues with the plan, the periods, the amount, the due date and the notes, then reports and closes', async () => {
    const w = await open()
    await type('input[name=periods]', '2')
    await type('textarea[name=notes]', 'Discount agreed')
    await submit()
    expect(POST).toHaveBeenCalledWith('/api/v1/platform/tenants/{code}/invoices', {
      params: { path: { code: 'SGR' } },
      body: { plan_code: 'PRO', periods: 2, amount: '15000000', due_on: '2027-02-09', notes: 'Discount agreed' },
    })
    expect(w.emitted('issued')).toHaveLength(1)
    expect(w.emitted('update:open')?.[0]).toEqual([false])
  })

  it('leaves the notes out when there are none', async () => {
    await open()
    await submit()
    expect(POST.mock.calls[0]![1].body).not.toHaveProperty('notes')
  })

  it('shows a refusal of the server inline and stays open', async () => {
    POST = vi.fn(async () => {
      throw new ApiError({ type: 'x', title: 'An open invoice already covers part of this period.', status: 409, code: 'INVOICE_PERIOD_OVERLAP' })
    })
    const w = await open()
    await submit()
    expect(q('[data-testid=form-error]').textContent).toContain('already covers part of this period')
    expect(w.emitted('issued')).toBeUndefined()
    expect(w.emitted('update:open')).toBeUndefined()
  })

  it('shows a failed preview inline and empties the preview', async () => {
    GET = vi.fn(async () => {
      throw new ApiError({ type: 'x', title: 'No tenant with this code.', status: 404, code: 'TENANT_NOT_FOUND' })
    })
    await open()
    expect(q('[data-testid=form-error]').textContent).toContain('No tenant with this code.')
    expect(q('[data-testid=preview-number]').textContent).toBe('–')
  })

  it('closes on Escape without sending', async () => {
    const w = await open()
    q('[data-testid=issue-invoice-dialog]').dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    await flushPromises()
    expect(w.emitted('update:open')?.[0]).toEqual([false])
    expect(POST).not.toHaveBeenCalled()
  })
})
