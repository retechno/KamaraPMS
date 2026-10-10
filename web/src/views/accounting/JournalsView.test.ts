import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { createMemoryHistory, createRouter } from 'vue-router'
import { fromMilli, toMilli, totals } from './accountMeta'
import JournalsView from './JournalsView.vue'

let GET = vi.fn()
let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a) } }))

const journal = (over: Record<string, unknown>) => ({
  id: 1, journal_number: 'JV000001', journal_type: 'MANUAL', journal_date: '2026-09-30', description: 'Cash sale', reference: 'INV-1', reverses_journal_id: null,
  reversed_by_journal_id: null, posted_at: '2026-09-30T10:00:00Z', posted_by: 5, approved_by: null, total: '100000', line_count: 2, ...over,
})
const lines = [
  { line_no: 1, account_id: 1, account_code: '1110', account_name: 'Cash on hand', debit: '100000', credit: '0' },
  { line_no: 2, account_id: 2, account_code: '4110', account_name: 'Room revenue', debit: '0', credit: '100000', description: 'rooms' },
]
const list = [
  journal({}),
  journal({ id: 2, journal_number: 'JV000002', journal_type: 'DAY_CLOSE', description: 'Day close 2026-09-30', reference: undefined }),
  journal({ id: 3, journal_number: 'JV000003', reversed_by_journal_id: 9, reversed_by_number: 'JV000009' }),
]
const accounts = [
  { id: 1, code: '1110', name: 'Cash on hand', account_type: 'ASSET', is_postable: true, is_active: true },
  { id: 2, code: '4110', name: 'Room revenue', account_type: 'REVENUE', is_postable: true, is_active: true },
  { id: 3, code: '1100', name: 'Cash header', account_type: 'ASSET', is_postable: false, is_active: true },
]

function mountView(permissions = ['accounting.view', 'accounting.post', 'accounting.close']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'a@b.c', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn(async (path: string) => {
    if (path.endsWith('/journals/{id}')) return { data: journal({ lines }) }
    if (path.endsWith('/journals')) return { data: { data: list } }
    return { data: { data: accounts } }
  })
  POST = vi.fn().mockResolvedValue({ data: journal({ id: 4, journal_number: 'JV000004' }) })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  return mount(JournalsView, { global: { plugins: [pinia, router], stubs: { ApprovalDialog: { name: 'ApprovalDialog', emits: ['approve', 'cancel'], template: '<div data-testid="approval" />' } } } })
}

/** Runs `fn` with the window of a phone (390 px), then puts the width back. */
async function onAPhone<T>(fn: () => Promise<T>): Promise<T> {
  const wide = window.innerWidth
  Object.defineProperty(window, 'innerWidth', { configurable: true, value: 390 })
  try {
    return await fn()
  } finally {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: wide })
  }
}

describe('decimal helpers', () => {
  it('count in thousandths without floats', () => {
    expect(toMilli('100000.5')).toBe(100000500n)
    expect(toMilli('-1.25')).toBe(-1250n)
    expect(toMilli('1,5')).toBeNull()
    expect(toMilli('1.2345')).toBeNull()
    expect(fromMilli(100000500n)).toBe('100000.5')
    expect(fromMilli(-1250n)).toBe('-1.25')
    expect(fromMilli(0n)).toBe('0')
    const t = totals([{ debit: '0.1', credit: '' }, { debit: '0.2', credit: '' }, { debit: '', credit: '0.3' }])
    expect(t).toEqual({ debit: 300n, credit: 300n, valid: true })
    expect(totals([{ debit: 'x', credit: '' }]).valid).toBe(false)
  })
})

describe('JournalsView', () => {
  beforeEach(() => {
    GET = vi.fn()
    POST = vi.fn()
  })

  it('lists journals and shows the lines of the one opened', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.get('[data-testid=journal-JV000002]').text()).toContain('Day close')
    expect(w.get('[data-testid=journal-JV000003]').text()).toContain('Reversed by JV000009')
    await w.get('[data-testid=journal-JV000001]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid=journal-detail]').text()).toContain('4110 · Room revenue')
    expect(GET.mock.calls.at(-1)?.[1].params.path).toEqual({ propertyId: 7, id: 1 })
    // each line links to the ledger of its account for the month of the journal
    expect(w.get('[data-testid=ledger-link-2]').attributes('href')).toBe('/accounting/ledger?account=2&from=2026-09-01&to=2026-09-30')
    await w.get('[data-testid=journal-JV000001]').trigger('click')
    expect(w.find('[data-testid=journal-detail]').exists()).toBe(false)
  })

  it('filters on the server', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('input[name=from]').setValue('2026-09-01')
    await w.get('select[name=type]').setValue('MANUAL')
    await w.get('input[name=q]').setValue(' cash ')
    await w.get('[data-testid=apply]').trigger('submit')
    await flushPromises()
    const call = GET.mock.calls.filter((c) => String(c[0]).endsWith('/journals')).at(-1)
    expect(call?.[1].params.query).toEqual({ from: '2026-09-01', to: undefined, type: 'MANUAL', q: 'cash' })
  })

  it('posts a journal only when it balances, with an idempotency key', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=new-journal]').trigger('click')
    expect(w.findAll('select[name=account_0] option').map((o) => o.text())).toEqual(['Choose an account', '1110 · Cash on hand', '4110 · Room revenue'])
    await w.get('input[name=date]').setValue('2026-09-30')
    await w.get('input[name=description]').setValue('Cash sale')
    await w.get('select[name=account_0]').setValue(1)
    await w.get('select[name=account_1]').setValue(2)
    await w.get('input[name=debit_0]').setValue('100000')
    await w.get('input[name=credit_1]').setValue('90000')
    expect(w.get('[data-testid=difference]').text()).toContain('10,000')
    expect((w.get('[data-testid=journal-post]').element as HTMLButtonElement).disabled).toBe(true)
    await w.get('input[name=credit_1]').setValue('100000')
    expect((w.get('input[name=credit_0]').element as HTMLInputElement).disabled).toBe(true) // a line has one side
    expect((w.get('[data-testid=journal-post]').element as HTMLButtonElement).disabled).toBe(false)
    await w.get('[data-testid=journal-form]').trigger('submit')
    await flushPromises()
    const [path, init] = POST.mock.calls[0] as [string, { params: { header: Record<string, string> }; body: unknown }]
    expect(path).toBe('/api/v1/properties/{propertyId}/accounting/journals')
    expect(init.params.header['Idempotency-Key']).toBeTruthy()
    expect(init.body).toEqual({
      journal_date: '2026-09-30', description: 'Cash sale', reference: undefined,
      lines: [{ account_id: 1, debit: '100000', credit: undefined, description: undefined }, { account_id: 2, debit: undefined, credit: '100000', description: undefined }],
    })
    expect(w.get('[data-testid=notice]').text()).toContain('JV000004')
    expect(w.find('[data-testid=journal-form]').exists()).toBe(false)
  })

  it('shows what the server refuses on a line', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=new-journal]').trigger('click')
    await w.get('input[name=date]').setValue('2026-09-30')
    await w.get('input[name=description]').setValue('x')
    await w.get('select[name=account_0]').setValue(1)
    await w.get('select[name=account_1]').setValue(2)
    await w.get('input[name=debit_0]').setValue('5')
    await w.get('input[name=credit_1]').setValue('5')
    POST.mockRejectedValue(new ApiError({ type: 't', title: 'Invalid', status: 422, code: 'VALIDATION_FAILED', detail: 'the journal is invalid', errors: [{ field: 'lines[0].account_id', code: 'CONTROL_ACCOUNT', message: 'only the day close posts to the guest ledger account' }] } as never))
    await w.get('[data-testid=journal-form]').trigger('submit')
    await flushPromises()
    expect(w.get('[data-testid=journal-form]').text()).toContain('only the day close posts')
    await w.get('[data-testid=add-line]').trigger('click')
    expect(w.findAll('tbody tr[data-testid^=line-]')).toHaveLength(3)
    await w.get('[data-testid=remove-line-2]').trigger('click')
    expect(w.findAll('tbody tr[data-testid^=line-]')).toHaveLength(2)
  })

  it('reverses a manual journal with a reason and an approval', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=journal-JV000001]').trigger('click')
    await flushPromises()
    await w.get('[data-testid=reverse]').trigger('click')
    expect((w.get('[data-testid=reverse-ask]').element as HTMLButtonElement).disabled).toBe(true)
    await w.get('input[name=reason]').setValue('wrong account')
    await w.get('[data-testid=reverse-ask]').trigger('submit')
    await flushPromises()
    const dialog = w.findComponent({ name: 'ApprovalDialog' })
    expect(dialog.exists()).toBe(true)
    dialog.vm.$emit('approve', { email: 'a@b.c', password: 'pw' })
    await flushPromises()
    const [path, init] = POST.mock.calls.at(-1) as [string, { params: { path: unknown }; body: unknown }]
    expect(path).toBe('/api/v1/properties/{propertyId}/accounting/journals/{id}/reverse')
    expect(init.params.path).toEqual({ propertyId: 7, id: 1 })
    expect(init.body).toEqual({ reason: 'wrong account', approval: { email: 'a@b.c', password: 'pw' } })
    expect(w.get('[data-testid=notice]').text()).toContain('JV000001 reversed')
  })

  it('journals the days that are missing', async () => {
    const w = mountView()
    await flushPromises()
    POST.mockResolvedValue({ data: { posted: 3 } })
    await w.get('[data-testid=post-pending]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid=notice]').text()).toContain('3 business day(s) journaled')
  })

  it('shows only what the role may do', async () => {
    const viewer = mountView(['accounting.view'])
    await flushPromises()
    expect(viewer.find('[data-testid=new-journal]').exists()).toBe(false)
    expect(viewer.find('[data-testid=post-pending]').exists()).toBe(false)
    await viewer.get('[data-testid=journal-JV000001]').trigger('click')
    await flushPromises()
    expect(viewer.find('[data-testid=reverse]').exists()).toBe(false)
    const none = mountView([])
    await flushPromises()
    expect(none.find('[data-testid=no-access]').exists()).toBe(true)
    expect(GET).not.toHaveBeenCalled()
  })

  it('is a card on a phone: the number as the title, the description under it, the type in the corner and the total at the right; a tap opens it', async () => {
    await onAPhone(async () => {
      const w = mountView()
      await flushPromises()
      expect(w.find('table').exists()).toBe(false)
      const card = w.get('[data-testid=journal-JV000001]')
      expect(card.text()).toContain('JV000001')
      expect(card.text()).toContain('Cash sale')
      expect(card.text()).toContain('Manual')
      expect(card.get('p.font-semibold').text()).toBe('100,000')
      await card.trigger('click')
      await flushPromises()
      expect(w.get('[data-testid=journal-detail]').text()).toContain('1110')
      w.unmount()
    })
  })

  it('has a short bar on a phone: the search, and "Filter (n)" for the dates and the type', async () => {
    await onAPhone(async () => {
      const w = mountView()
      await flushPromises()
      expect(w.find('input[name=q]').exists()).toBe(true)
      expect(w.find('input[name=from]').exists()).toBe(false)
      expect(w.get('[data-testid=open-filters]').text()).toBe('Filter')
      await w.get('[data-testid=open-filters]').trigger('click')
      await flushPromises()
      const sheet = document.body.querySelector('[data-testid=filter-sheet]')!
      const type = sheet.querySelector('select[name=type]') as HTMLSelectElement
      type.value = 'MANUAL'
      type.dispatchEvent(new Event('change'))
      await flushPromises()
      expect(w.get('[data-testid=filter-count]').text()).toBe('(1)')
      expect(sheet.querySelector('[data-testid=apply]')).not.toBeNull() // the button of the page is in the sheet
      w.unmount()
      document.body.innerHTML = ''
    })
  })
})
