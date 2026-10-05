import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import BankAccountsView from './BankAccountsView.vue'
import BankReconcileView from './BankReconcileView.vue'
import BankStatementsView from './BankStatementsView.vue'

let GET = vi.fn()
let POST = vi.fn()
let PATCH = vi.fn()
let DELETE = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a), PATCH: (...a: unknown[]) => PATCH(...a), DELETE: (...a: unknown[]) => DELETE(...a) } }))

const bank = (over: Record<string, unknown> = {}) => ({
  id: 1, account_id: 3, account_code: '1130', account_name: 'Bank - operating account', name: 'BCA operating', account_number: '123-456', is_active: true, book_balance: '750000',
  reconciled_to: null, open_statements: 1, created_at: '2026-09-30T00:00:00Z', ...over,
})
const statementRow = (over: Record<string, unknown> = {}) => ({
  id: 5, bank_account_id: 1, bank_name: 'BCA operating', account_code: '1130', period_from: '2026-09-01', period_to: '2026-09-30', opening_balance: '0', closing_balance: '735000', status: 'OPEN',
  line_count: 3, matched_count: 1, imported_at: '2026-10-01T00:00:00Z', reconciled_at: null, ...over,
})
const accounts = [
  { id: 3, code: '1130', name: 'Bank - operating account', account_type: 'ASSET', statement_group: 'CASH', is_postable: true, is_active: true },
  { id: 4, code: '1140', name: 'Bank - payroll account', account_type: 'ASSET', statement_group: 'CASH', is_postable: true, is_active: true },
  { id: 8, code: '4110', name: 'Room revenue', account_type: 'REVENUE', statement_group: 'REV_ROOMS', is_postable: true, is_active: true },
  { id: 9, code: '6130', name: 'Credit card commissions', account_type: 'EXPENSE', statement_group: 'UND_AG', is_postable: true, is_active: true },
]
const summary = (over: Record<string, unknown> = {}) => ({
  statement_closing: '735000', book_balance: '750000', cleared_total: '750000', uncleared_in: '0', uncleared_out: '0', uncleared_count: 0, unmatched_lines: 1, unmatched_amount: '-15000',
  adjusted_bank: '735000', difference: '-15000', blockers: ['Some statement lines are not matched with the books.'], can_reconcile: false, ...over,
})
const detail = (over: Record<string, unknown> = {}) => ({
  ...statementRow(), clearings: [], summary: summary(),
  lines: [
    { id: 11, line_no: 1, line_date: '2026-09-30', description: 'Transfer from guest', reference: 'TRF1', amount: '1000000', cleared: '1000000', matched: true, clearings: [{ id: 21, statement_line_id: 11, journal_line_id: 101, amount: '1000000', journal_date: '2026-09-30', journal_number: 'JV000002', journal_type: 'MANUAL' }] },
    { id: 12, line_no: 2, line_date: '2026-09-30', description: 'Payment PLN', amount: '-250000', cleared: '0', matched: false, clearings: [] },
    { id: 13, line_no: 3, line_date: '2026-09-30', description: 'Monthly bank fee', amount: '-15000', cleared: '0', matched: false, clearings: [] },
  ],
  ...over,
})
const unclearedList = [{ journal_line_id: 102, journal_date: '2026-09-30', journal_id: 7, journal_number: 'JV000003', journal_type: 'MANUAL', description: 'book pay', amount: '-250000', cleared: '0', remaining: '-250000' }]
const settleList = [
  { journal_line_id: 301, journal_date: '2026-09-30', journal_id: 8, journal_number: 'JV000008', journal_type: 'DAY_CLOSE', description: 'Payment PAY1 · AUTH-1', amount: '400000', cleared: '0', remaining: '400000' },
  { journal_line_id: 302, journal_date: '2026-09-30', journal_id: 8, journal_number: 'JV000008', journal_type: 'DAY_CLOSE', description: 'Payment PAY2 · AUTH-2', amount: '600000', cleared: '0', remaining: '600000' },
]

function mountView(component: object, permissions = ['bank.view', 'bank.manage', 'bank.reconcile', 'accounting.view'], props: Record<string, unknown> = {}, path = '/') {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'a@b.c', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn(async (p: string) => {
    if (p.endsWith('/uncleared')) return { data: { data: unclearedList } }
    if (p.endsWith('/settlement-lines')) return { data: { data: settleList } }
    if (p.endsWith('/statements/{id}')) return { data: detail() }
    if (p.endsWith('/statements')) return { data: { data: [statementRow(), statementRow({ id: 4, status: 'RECONCILED', period_from: '2026-08-01', period_to: '2026-08-31', matched_count: 2, line_count: 2, reconciled_at: '2026-09-02T00:00:00Z' })] } }
    if (p.endsWith('/bank/accounts')) return { data: { data: [bank()] } }
    return { data: { data: accounts } }
  })
  POST = vi.fn().mockResolvedValue({ data: detail() })
  PATCH = vi.fn().mockResolvedValue({ data: bank() })
  DELETE = vi.fn().mockResolvedValue({ data: detail() })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  return router.push(path).then(async () => {
    await router.isReady()
    return mount(component as never, { props: props as never, global: { plugins: [pinia, router], stubs: { ApprovalDialog: { name: 'ApprovalDialog', emits: ['approve', 'cancel'], template: '<div data-testid="approval" />' } } } })
  })
}

describe('bank views', () => {
  beforeEach(() => {
    GET = vi.fn()
    POST = vi.fn()
    PATCH = vi.fn()
    DELETE = vi.fn()
    vi.restoreAllMocks()
  })

  it('lists the bank accounts and registers a cash or bank account of the books', async () => {
    const w = await mountView(BankAccountsView)
    await flushPromises()
    expect(w.get('[data-testid=bank-1130]').text()).toContain('750,000')
    expect(w.get('[data-testid=bank-1130]').text()).toContain('never')
    await w.get('[data-testid=new-bank]').trigger('click')
    expect(w.findAll('select[name=account_id] option').map((o) => o.text())).toEqual(['Choose an account', '1140 · Bank - payroll account']) // not the registered one, not revenue
    await w.get('select[name=account_id]').setValue(4)
    await w.get('input[name=name]').setValue('BCA payroll')
    await w.get('[data-testid=bank-form]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls[0]?.[1].body).toEqual({ account_id: 4, name: 'BCA payroll', account_number: undefined, is_active: true })
    await w.get('[data-testid=edit-1130]').trigger('click')
    PATCH.mockRejectedValue(new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'BANK_ACCOUNT_EXISTS', detail: 'registered already' }))
    await w.get('[data-testid=bank-form]').trigger('submit')
    await flushPromises()
    expect(PATCH.mock.calls[0]?.[1].params.path).toEqual({ propertyId: 7, id: 1 })
    expect(w.get('[data-testid=bank-error]').text()).toContain('BANK_ACCOUNT_EXISTS')
  })

  it('imports a statement that follows the last one and shows the rows that are wrong', async () => {
    const w = await mountView(BankStatementsView, undefined, {}, '/bank/statements?bank=1')
    await flushPromises()
    expect(w.get('[data-testid=statement-5]').text()).toContain('1 of 3')
    expect(w.get('[data-testid=statement-4]').text()).toContain('Reconciled')
    expect(w.find('[data-testid=delete-4]').exists()).toBe(false) // a reconciled statement is not deleted
    await w.get('[data-testid=new-statement]').trigger('click')
    expect((w.get('input[name=period_from]').element as HTMLInputElement).value).toBe('2026-10-01') // the day after the latest one
    expect((w.get('input[name=opening_balance]').element as HTMLInputElement).value).toBe('735000')
    await w.get('input[name=period_to]').setValue('2026-10-31')
    await w.get('input[name=closing_balance]').setValue('735100')
    await w.get('textarea[name=csv]').setValue('date,amount\n2026-10-02,100\n')
    await w.get('[data-testid=import-form]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls[0]?.[1].body).toEqual({
      bank_account_id: 1, period_from: '2026-10-01', period_to: '2026-10-31', opening_balance: '735000', closing_balance: '735100', note: undefined, csv: 'date,amount\n2026-10-02,100\n',
    })
    expect(w.get('[data-testid=notice]').text()).toContain('imported')
    await w.get('[data-testid=new-statement]').trigger('click')
    await w.get('input[name=period_to]').setValue('2026-11-30')
    await w.get('input[name=closing_balance]').setValue('1')
    await w.get('textarea[name=csv]').setValue('x')
    POST.mockRejectedValue(new ApiError({ type: 't', title: 'Invalid', status: 422, code: 'VALIDATION_FAILED', detail: 'the statement has rows that cannot be read', errors: [{ field: 'rows[3].amount', code: 'INVALID_AMOUNT', message: 'a number' }] } as never))
    await w.get('[data-testid=import-form]').trigger('submit')
    await flushPromises()
    expect(w.get('[data-testid=import-error]').text()).toContain('rows[3].amount: a number')
  })

  it('deletes an open statement after asking', async () => {
    const w = await mountView(BankStatementsView, undefined, {}, '/bank/statements?bank=1')
    await flushPromises()
    vi.spyOn(window, 'confirm').mockReturnValueOnce(false)
    await w.get('[data-testid=delete-5]').trigger('click')
    expect(DELETE).not.toHaveBeenCalled()
    vi.spyOn(window, 'confirm').mockReturnValueOnce(true)
    await w.get('[data-testid=delete-5]').trigger('click')
    await flushPromises()
    expect(DELETE.mock.calls[0]?.[1].params.path).toEqual({ propertyId: 7, id: 5 })
  })

  it('matches journal lines with a statement line and posts what the books lack', async () => {
    const w = await mountView(BankReconcileView, undefined, { id: '5' })
    await flushPromises()
    expect(w.get('[data-testid=difference]').text()).toBe('-15,000')
    expect(w.get('[data-testid=blockers]').text()).toContain('not matched')
    expect((w.get('[data-testid=reconcile]').element as HTMLButtonElement).disabled).toBe(true)
    expect(w.get('[data-testid=line-1]').classes()).toContain('done')
    // pick a statement line and a journal line, then match
    await w.get('[data-testid=pick-line-2]').setValue(true)
    await w.get('[data-testid=uncleared-102] input').setValue(true)
    expect(w.get('[data-testid=picked-total]').text()).toContain('-250,000')
    expect(w.get('[data-testid=match]').text()).toContain('Match with line 2')
    await w.get('[data-testid=match]').trigger('click')
    await flushPromises()
    expect(POST.mock.calls[0]?.[0]).toBe('/api/v1/properties/{propertyId}/bank/statements/{id}/clearings')
    expect(POST.mock.calls[0]?.[1].body).toEqual({ allocations: [{ statement_line_id: 12, journal_line_id: 102, amount: '-250000' }] })
    // post the bank fee
    await w.get('[data-testid=pick-line-2]').setValue(false)
    await w.get('[data-testid=pick-line-3]').setValue(true)
    await w.get('[data-testid=adjust-open]').trigger('click')
    expect((w.get('[data-testid=adjust-post]').element as HTMLButtonElement).disabled).toBe(true)
    await w.get('select[name=adjust_account]').setValue(9)
    await w.get('[data-testid=adjust-form]').trigger('submit')
    await flushPromises()
    const call = POST.mock.calls.at(-1) as [string, { params: { path: unknown }; body: unknown }]
    expect(call[0]).toBe('/api/v1/properties/{propertyId}/bank/statements/{id}/lines/{lineId}/adjust')
    expect(call[1].params.path).toEqual({ propertyId: 7, id: 5, lineId: 13 })
    expect(call[1].body).toEqual({ account_id: 9, department_id: null, description: undefined })
  })

  it('spreads one journal line over several statement lines, each taking what it needs', async () => {
    const lines = [
      { id: 31, line_no: 1, line_date: '2026-09-30', description: 'Transfer 1', amount: '100000', cleared: '0', matched: false, clearings: [] },
      { id: 32, line_no: 2, line_date: '2026-09-30', description: 'Transfer 2', amount: '200000', cleared: '0', matched: false, clearings: [] },
      { id: 33, line_no: 3, line_date: '2026-09-30', description: 'Transfer 3', amount: '300000', cleared: '0', matched: false, clearings: [] },
    ]
    const w = await mountView(BankReconcileView, undefined, { id: '5' })
    await flushPromises()
    const total = { journal_line_id: 201, journal_date: '2026-09-30', journal_id: 9, journal_number: 'JV000009', journal_type: 'DAY_CLOSE', description: 'Payments BANK_TRANSFER', amount: '600000', cleared: '100000', remaining: '500000' }
    GET.mockImplementation(async (p: string) => (p.endsWith('/uncleared') ? { data: { data: [total] } } : { data: detail({ lines }) }))
    await (w as unknown as { setProps: (p: object) => Promise<void> }).setProps({ id: '6' })
    await flushPromises()
    expect(w.get('[data-testid=uncleared-201]').text()).toContain('500,000 of 600,000') // what is left of a line cleared in part
    await w.get('[data-testid=uncleared-201] input').setValue(true)
    await w.get('[data-testid=pick-line-1]').setValue(true)
    await w.get('[data-testid=pick-line-2]').setValue(true)
    expect(w.get('[data-testid=match]').text()).toContain('Match with 2 lines')
    expect(w.get('[data-testid=spread-hint]').text()).toContain('300,000 to match')
    await w.get('[data-testid=match]').trigger('click')
    await flushPromises()
    expect(POST.mock.calls.at(-1)?.[1].body).toEqual({
      allocations: [{ statement_line_id: 31, journal_line_id: 201, amount: '100000' }, { statement_line_id: 32, journal_line_id: 201, amount: '200000' }],
    })
  })

  it('settles card payments from a statement line net of the commission', async () => {
    const w = await mountView(BankReconcileView, undefined, { id: '5' })
    await flushPromises()
    expect(w.find('[data-testid=settle-open]').exists()).toBe(false) // nothing selected
    await w.get('[data-testid=pick-line-3]').setValue(true) // money out: no settlement
    expect(w.find('[data-testid=settle-open]').exists()).toBe(false)
    await w.get('[data-testid=pick-line-3]').setValue(false)
    // an unmatched line of money in
    const lines = detail().lines.map((l) => (l.id === 12 ? { ...l, amount: '980000', description: 'Card settlement' } : l))
    GET.mockImplementation(async (p: string) => {
      if (p.endsWith('/uncleared')) return { data: { data: unclearedList } }
      if (p.endsWith('/settlement-lines')) return { data: { data: settleList } }
      return { data: detail({ lines }) }
    })
    await (w as unknown as { setProps: (p: object) => Promise<void> }).setProps({ id: '6' })
    await flushPromises()
    await w.get('[data-testid=pick-line-2]').setValue(true)
    await w.get('[data-testid=settle-open]').trigger('click')
    await flushPromises()
    expect(GET.mock.calls.some((c) => String(c[0]).endsWith('/settlement-lines') && c[1].params.query.account_key === 'CARD')).toBe(true)
    expect((w.get('[data-testid=settle-post]').element as HTMLButtonElement).disabled).toBe(true)
    await w.get('[data-testid=settle-301] input').setValue(true)
    expect((w.get('[data-testid=settle-post]').element as HTMLButtonElement).disabled).toBe(true) // the payments chosen are less than was paid out
    expect(w.get('[data-testid=settle-summary]').text()).toContain('Payments 400,000')
    await w.get('[data-testid=settle-302] input').setValue(true)
    expect(w.get('[data-testid=settle-summary]').text()).toContain('commission 20,000')
    expect((w.get('[data-testid=settle-post]').element as HTMLButtonElement).disabled).toBe(true) // a commission needs its account
    await w.get('select[name=settle_fee]').setValue(9)
    await w.get('input[name=settle_description]').setValue('Card settlement 1 Oct')
    await w.get('[data-testid=settle-form]').trigger('submit')
    await flushPromises()
    const call = POST.mock.calls.at(-1) as [string, { params: { path: unknown }; body: unknown }]
    expect(call[0]).toBe('/api/v1/properties/{propertyId}/bank/statements/{id}/lines/{lineId}/settle')
    expect(call[1].params.path).toEqual({ propertyId: 7, id: 6, lineId: 12 })
    expect(call[1].body).toEqual({ account_key: 'CARD', journal_line_ids: [301, 302], fee_account_id: 9, department_id: null, description: 'Card settlement 1 Oct' })
  })

  it('suggests the payments of a settlement and picks them', async () => {
    const w = await mountView(BankReconcileView, undefined, { id: '5' })
    await flushPromises()
    const lines = detail().lines.map((l) => (l.id === 12 ? { ...l, amount: '980000', description: 'Card settlement' } : l))
    GET.mockImplementation(async (p: string) => {
      if (p.endsWith('/uncleared')) return { data: { data: unclearedList } }
      if (p.endsWith('/settlement-lines')) return { data: { data: settleList } }
      if (p.endsWith('/settlement-proposal')) return { data: { matched: true, difference: '0', expected_mdr: '20000', journal_line_ids: [301, 302] } }
      return { data: detail({ lines }) }
    })
    await (w as unknown as { setProps: (p: object) => Promise<void> }).setProps({ id: '6' })
    await flushPromises()
    await w.get('[data-testid=pick-line-2]').setValue(true)
    await w.get('[data-testid=settle-open]').trigger('click')
    await flushPromises()
    await w.get('[data-testid=suggest]').trigger('click')
    await flushPromises()
    const call = GET.mock.calls.find((c) => String(c[0]).endsWith('/settlement-proposal')) as [string, { params: { path: unknown; query: unknown } }]
    expect(call[1].params.path).toEqual({ propertyId: 7, id: 6, lineId: 12 })
    expect(call[1].params.query).toEqual({ account_key: 'CARD' })
    expect(w.get('[data-testid=suggestion]').text()).toContain('20,000')
    expect(w.get('[data-testid=settle-summary]').text()).toContain('Payments 1,000,000')
  })

  it('undoes a matching, matches automatically and clears lines without a statement line', async () => {
    const w = await mountView(BankReconcileView, undefined, { id: '5' })
    await flushPromises()
    await w.get('[data-testid=unmatch-21]').trigger('click')
    await flushPromises()
    expect(DELETE.mock.calls[0]?.[1].params.path).toEqual({ propertyId: 7, id: 5, clearingId: 21 })
    POST.mockResolvedValue({ data: { matched: 2, remaining: 1 } })
    await w.get('[data-testid=auto-match]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid=notice]').text()).toContain('2 line(s) matched, 1 left')
    POST.mockResolvedValue({ data: detail() })
    await w.get('[data-testid=uncleared-102] input').setValue(true)
    expect(w.get('[data-testid=match]').text()).toContain('Clear without a statement line')
    await w.get('[data-testid=match]').trigger('click')
    await flushPromises()
    expect(POST.mock.calls.at(-1)?.[1].body).toEqual({ statement_line_id: null, journal_line_ids: [102] })
  })

  it('reconciles when ready, and reopens a reconciled statement with an approval', async () => {
    const ready = detail({ summary: summary({ blockers: [], can_reconcile: true, unmatched_lines: 0, difference: '0' }) })
    const w = await mountView(BankReconcileView, undefined, { id: '5' })
    GET.mockImplementation(async (p: string) => (p.endsWith('/uncleared') ? { data: { data: [] } } : { data: ready }))
    await (w as unknown as { setProps: (p: object) => Promise<void> }).setProps({ id: '6' })
    await flushPromises()
    expect(w.find('[data-testid=ready]').exists()).toBe(true)
    await w.get('[data-testid=reconcile]').trigger('click')
    await flushPromises()
    expect(POST.mock.calls.at(-1)?.[0]).toBe('/api/v1/properties/{propertyId}/bank/statements/{id}/reconcile')
    // a reconciled statement is read-only and can be reopened
    const done = detail({ status: 'RECONCILED', reconciled_at: '2026-10-02T00:00:00Z', summary: summary({ blockers: [], can_reconcile: false }) })
    GET.mockImplementation(async (p: string) => (p.endsWith('/uncleared') ? { data: { data: [] } } : { data: done }))
    await (w as unknown as { setProps: (p: object) => Promise<void> }).setProps({ id: '7' })
    await flushPromises()
    expect(w.get('[data-testid=reconciled]').text()).toContain('final')
    expect(w.find('[data-testid=reconcile]').exists()).toBe(false)
    expect(w.find('[data-testid=pick-line-2]').exists()).toBe(false)
    await w.get('[data-testid=reopen]').trigger('click')
    expect((w.get('[data-testid=reopen-ask]').element as HTMLButtonElement).disabled).toBe(true)
    await w.get('input[name=reason]').setValue('missed a fee')
    await w.get('[data-testid=reopen-form]').trigger('submit')
    w.findComponent({ name: 'ApprovalDialog' }).vm.$emit('approve', { email: 'a@b.c', password: 'pw' })
    await flushPromises()
    expect(POST.mock.calls.at(-1)?.[0]).toBe('/api/v1/properties/{propertyId}/bank/statements/{id}/reopen')
    expect(POST.mock.calls.at(-1)?.[1].body).toEqual({ reason: 'missed a fee', approval: { email: 'a@b.c', password: 'pw' } })
  })

  it('shows only what the role may do', async () => {
    const viewer = ['bank.view']
    const s = await mountView(BankStatementsView, viewer, {}, '/bank/statements?bank=1')
    await flushPromises()
    expect(s.find('[data-testid=new-statement]').exists()).toBe(false)
    expect(s.find('[data-testid=delete-5]').exists()).toBe(false)
    expect(s.get('[data-testid=open-5]').text()).toBe('View')
    const r = await mountView(BankReconcileView, viewer, { id: '5' })
    await flushPromises()
    expect(r.find('[data-testid=auto-match]').exists()).toBe(false)
    expect(r.find('[data-testid=pick-line-2]').exists()).toBe(false)
    const b = await mountView(BankAccountsView, viewer)
    await flushPromises()
    expect(b.find('[data-testid=new-bank]').exists()).toBe(false)
    for (const component of [BankAccountsView, BankStatementsView, BankReconcileView]) {
      const none = await mountView(component, [], { id: '5' })
      await flushPromises()
      expect(none.find('[data-testid=no-access]').exists()).toBe(true)
      expect(GET).not.toHaveBeenCalled()
    }
  })
})
