import { DOMWrapper, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import ReturnDialog from './ReturnDialog.vue'
import TransferDialog from './TransferDialog.vue'
import TransferredList from './TransferredList.vue'

let GET = vi.fn()
let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a) } }))

const body = (sel: string) => new DOMWrapper(document.body.querySelector(sel) as Element)
const has = (sel: string) => document.body.querySelector(sel) !== null
const folio = { id: 3, folio_number: 'FOL000001', folio_type: 'GUEST', status: 'OPEN', reservation_id: 9, stay_id: 4, opened_at: '', closed_at: null, version: 1, balance: '122100', totals: { debit: '122100', credit: '0' }, items: [] } as never
const item = (id: number) => ({ id, debit: '122100', credit: '0', description: 'Minibar', charge_code: 'MINIBAR', charge_code_id: 2 }) as never
const targets = [
  { id: 8, folio_number: 'FOL000002', folio_type: 'COMPANY', confirmation_number: 'RES1', room: '101', guest: 'Siti', company: 'Acme', balance: '0', scope: 'RESERVATION' },
  { id: 9, folio_number: 'FOL000009', folio_type: 'GUEST', confirmation_number: 'RES2', room: '102', guest: 'Budi', balance: '5000', scope: 'ANY' },
]

const mounted: VueWrapper[] = []
function setupAuth(permissions = ['folio.read', 'folio.reverse']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'clerk@hotel.test', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  return { global: { plugins: [pinia, router] }, attachTo: document.body }
}
async function settle(): Promise<void> {
  await flushPromises()
  await vi.advanceTimersByTimeAsync(300)
  await flushPromises()
}
async function type(sel: string, value: string): Promise<void> {
  const el = document.body.querySelector(sel) as HTMLInputElement
  el.value = value
  el.dispatchEvent(new Event('input', { bubbles: true }))
  await flushPromises()
}
async function pickTarget(id: number): Promise<void> {
  const input = document.body.querySelector('input[role=combobox]') as HTMLInputElement
  input.focus()
  await settle()
  const option = [...document.body.querySelectorAll('[role=option]')].find((o) => o.textContent?.includes(targets.find((x) => x.id === id)?.folio_number ?? '')) as HTMLElement
  option.click()
  await flushPromises()
}
async function approveWith(email: string, password: string): Promise<void> {
  await type('input[name=approval_email]', email)
  await type('input[name=approval_password]', password)
  ;(document.body.querySelector('[data-testid=approval-dialog]') as HTMLFormElement).dispatchEvent(new Event('submit', { cancelable: true }))
  await flushPromises()
}

describe('TransferDialog', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    setLocale('en')
    GET = vi.fn(async (_p: string, o: { params: { query?: { q?: string } } }) => ({ data: { data: o.params.query?.q ? targets.filter((x) => x.room === o.params.query?.q) : targets } }))
    POST = vi.fn().mockResolvedValue({ data: { transfers: [{}, {}], batch_id: 1, balances: [], kind: 'ITEM', scope: 'RESERVATION', replayed: false } })
  })
  afterEach(() => {
    vi.useRealTimers()
    mounted.splice(0).forEach((w) => w.unmount())
    document.body.innerHTML = ''
  })

  it('moves the ticked charges: the folio is searched, the scope shown, the reason asked, then the approval', async () => {
    const w = mount(TransferDialog, { props: { folio, items: [item(1), item(2)], tab: 'items' }, ...setupAuth() })
    mounted.push(w)
    await settle()
    expect(body('[data-testid=transfer-selection]').text()).toBe('2 transactions selected (244,200)')
    expect(GET.mock.calls[0]?.[0]).toBe('/api/v1/properties/{propertyId}/folios/{id}/transfer-targets')
    expect(body('[data-testid=transfer-continue]').attributes('disabled')).toBeDefined()
    // searching asks the server, with the text typed
    await type('input[role=combobox]', '101')
    await settle()
    expect(GET.mock.calls.at(-1)?.[1].params.query.q).toBe('101')
    await pickTarget(8)
    expect(body('[data-testid=target-scope]').text()).toContain('same reservation')
    await type('input[name=transfer_reason]', 'the company pays the minibar')
    expect(body('[data-testid=transfer-continue]').attributes('disabled')).toBeUndefined()
    ;(document.body.querySelector('[data-testid=transfer-folio-form]') as HTMLFormElement).dispatchEvent(new Event('submit', { cancelable: true }))
    await flushPromises()
    expect(POST).not.toHaveBeenCalled() // the approval comes first
    await approveWith('boss@hotel.test', 'secret')
    const call = POST.mock.calls[0]
    expect(call?.[0]).toBe('/api/v1/properties/{propertyId}/folios/{id}/transfer-items')
    expect(call?.[1].body).toEqual({ folio_id: 8, item_ids: [1, 2], reason: 'the company pays the minibar', approval: { email: 'boss@hotel.test', password: 'secret' } })
    expect(w.emitted('done')?.[0]?.[0]).toBe('2 transactions moved to FOL000002.')
  })

  it('names the charge code when every charge of the code was picked', async () => {
    const w = mount(TransferDialog, { props: { folio, items: [item(1), item(2)], codeId: 2, tab: 'items' }, ...setupAuth() })
    mounted.push(w)
    await settle()
    expect(body('[data-testid=transfer-selection]').text()).toContain('Every transaction of the code')
    await pickTarget(8)
    await type('input[name=transfer_reason]', 'all of it')
    ;(document.body.querySelector('[data-testid=transfer-folio-form]') as HTMLFormElement).dispatchEvent(new Event('submit', { cancelable: true }))
    await flushPromises()
    await approveWith('boss@hotel.test', 'secret')
    expect(POST.mock.calls[0]?.[1].body).toMatchObject({ folio_id: 8, charge_code_id: 2 })
    expect(POST.mock.calls[0]?.[1].body.item_ids).toBeUndefined()
  })

  it('hands a balance over with a key, and warns about the tax invoice', async () => {
    const w = mount(TransferDialog, { props: { folio, items: [], tab: 'balance' }, ...setupAuth() })
    mounted.push(w)
    await settle()
    expect(body('[data-testid=tax-warning]').text()).toBe("Need a tax invoice in the payer's name? Use transfer by transaction.")
    expect((document.body.querySelector('input[name=transfer_balance_amount]') as HTMLInputElement).value).toBe('122,100') // the balance, as the default, with thousands
    await type('input[name=transfer_balance_amount]', '50000')
    await pickTarget(8)
    await type('input[name=transfer_reason]', 'the company pays part')
    ;(document.body.querySelector('[data-testid=transfer-folio-form]') as HTMLFormElement).dispatchEvent(new Event('submit', { cancelable: true }))
    await flushPromises()
    await approveWith('boss@hotel.test', 'secret')
    const call = POST.mock.calls[0]
    expect(call?.[0]).toBe('/api/v1/properties/{propertyId}/folios/{id}/transfer-balance')
    expect(call?.[1].params.header['Idempotency-Key']).toBeTruthy()
    expect(call?.[1].body).toMatchObject({ folio_id: 8, amount: '50000', reason: 'the company pays part' })
  })

  it('says when the folio has no relation to this one, and what that needs', async () => {
    const w = mount(TransferDialog, { props: { folio, items: [item(1)], tab: 'items' }, ...setupAuth() })
    mounted.push(w)
    await settle()
    await pickTarget(9)
    expect(body('[data-testid=target-scope]').text()).toContain('unrelated folio')
    expect(body('[data-testid=any-warning]').text()).toContain('you do not hold the right')
    expect(body('[data-testid=any-warning]').text()).toContain('Folio FOL000009 is not related to FOL000001') // the two folios, by number
    w.unmount()
    document.body.innerHTML = ''
    const boss = mount(TransferDialog, { props: { folio, items: [item(1)], tab: 'items' }, ...setupAuth(['folio.read', 'folio.reverse', 'folio.transfer_any']) })
    mounted.push(boss)
    await settle()
    await pickTarget(9)
    expect(body('[data-testid=any-warning]').text()).toContain('approver who holds the right')
  })

  it('by transaction: the button follows what is typed, in any order, and says what is missing', async () => {
    const w = mount(TransferDialog, { props: { folio, items: [item(1)], tab: 'items' }, ...setupAuth() })
    mounted.push(w)
    await settle()
    const off = () => body('[data-testid=transfer-continue]').attributes('disabled') !== undefined
    expect(off()).toBe(true)
    expect(body('[data-testid=transfer-missing]').text()).toBe('Still needed: the folio to transfer to, a reason.')
    // the reason first, then the folio
    await type('input[name=transfer_reason]', 'x')
    expect(off()).toBe(true)
    expect(body('[data-testid=transfer-missing]').text()).toBe('Still needed: the folio to transfer to.')
    await pickTarget(8)
    expect(off()).toBe(false)
    expect(has('[data-testid=transfer-missing]')).toBe(false)
    // emptying the reason turns it off again, and typing turns it on
    await type('input[name=transfer_reason]', '   ')
    expect(off()).toBe(true)
    await type('input[name=transfer_reason]', 'y')
    expect(off()).toBe(false)
    // no charge ticked: nothing to move
    w.unmount()
    document.body.innerHTML = ''
    const none = mount(TransferDialog, { props: { folio, items: [], tab: 'items' }, ...setupAuth() })
    mounted.push(none)
    await settle()
    await pickTarget(8)
    await type('input[name=transfer_reason]', 'y')
    expect(body('[data-testid=transfer-continue]').attributes('disabled')).toBeDefined()
    expect(body('[data-testid=transfer-missing]').text()).toBe('Still needed: transactions to move.')
  })

  it('"Choose a folio" is the placeholder of the field, not an option that can be ticked', async () => {
    const w = mount(TransferDialog, { props: { folio, items: [item(1)], tab: 'items' }, ...setupAuth() })
    mounted.push(w)
    await settle()
    const input = document.body.querySelector('input[role=combobox]') as HTMLInputElement
    expect(input.placeholder).toBe('Choose a folio')
    expect(input.value).toBe('')
    input.focus()
    await settle()
    const options = [...document.body.querySelectorAll('[role=option]')].map((o) => o.textContent?.trim())
    expect(options.some((o) => o?.includes('Choose a folio'))).toBe(false)
    expect(options).toHaveLength(2)
  })

  it('the amount of a balance is shown with thousands, refuses zero and more than the balance, and sends the plain number', async () => {
    const w = mount(TransferDialog, { props: { folio, items: [], tab: 'balance' }, ...setupAuth() })
    mounted.push(w)
    await settle()
    const field = () => (document.body.querySelector('input[name=transfer_balance_amount]') as HTMLInputElement).value
    await pickTarget(8)
    await type('input[name=transfer_reason]', 'part')
    const off = () => body('[data-testid=transfer-continue]').attributes('disabled') !== undefined
    await type('input[name=transfer_balance_amount]', '1500000')
    expect(field()).toBe('1,500,000')
    expect(body('[data-testid=amount-problem]').text()).toBe('The amount is more than the balance of this folio (122,100).')
    expect(off()).toBe(true)
    await type('input[name=transfer_balance_amount]', '0')
    expect(body('[data-testid=amount-problem]').text()).toBe('The amount must be more than zero.')
    expect(off()).toBe(true)
    await type('input[name=transfer_balance_amount]', '-5000') // a minus is not a digit: it is not kept
    expect(field()).toBe('5,000')
    expect(off()).toBe(false)
    await type('input[name=transfer_balance_amount]', '')
    expect(off()).toBe(true)
    await type('input[name=transfer_balance_amount]', '122100')
    expect(off()).toBe(false) // the whole balance is allowed
    await type('input[name=transfer_balance_amount]', '75000')
    ;(document.body.querySelector('[data-testid=transfer-folio-form]') as HTMLFormElement).dispatchEvent(new Event('submit', { cancelable: true }))
    await flushPromises()
    await approveWith('boss@hotel.test', 'secret')
    expect(POST.mock.calls[0]?.[1].body.amount).toBe('75000')
  })

  it('in Indonesian the amount is grouped with dots and the comma is the decimal', async () => {
    setLocale('id')
    const w = mount(TransferDialog, { props: { folio, items: [], tab: 'balance' }, ...setupAuth() })
    mounted.push(w)
    await settle()
    const field = () => (document.body.querySelector('input[name=transfer_balance_amount]') as HTMLInputElement).value
    expect(field()).toBe('122.100')
    await type('input[name=transfer_balance_amount]', '2500,5')
    expect(field()).toBe('2.500,5')
    setLocale('en')
  })

  it('keeps the dialog on a refusal, so the password can be tried again', async () => {
    POST = vi.fn().mockRejectedValue(new ApiError({ code: 'APPROVAL_INVALID_CREDENTIALS', title: 'wrong', status: 401 } as never))
    const w = mount(TransferDialog, { props: { folio, items: [item(1)], tab: 'items' }, ...setupAuth() })
    mounted.push(w)
    await settle()
    await pickTarget(8)
    await type('input[name=transfer_reason]', 'x')
    ;(document.body.querySelector('[data-testid=transfer-folio-form]') as HTMLFormElement).dispatchEvent(new Event('submit', { cancelable: true }))
    await flushPromises()
    await approveWith('boss@hotel.test', 'bad')
    expect(has('[data-testid=approval-error]')).toBe(true)
    expect(w.emitted('done')).toBeUndefined()
  })

  it('says it in Indonesian', async () => {
    setLocale('id')
    const w = mount(TransferDialog, { props: { folio, items: [], tab: 'balance' }, ...setupAuth() })
    mounted.push(w)
    await settle()
    expect(body('[data-testid=tax-warning]').text()).toBe('Butuh faktur pajak atas nama pembayar? Gunakan transfer per transaksi.')
    expect(body('[data-testid=tab-transfer-items]').text()).toBe('Per transaksi')
    expect(body('[data-testid=tab-transfer-balance]').text()).toBe('Nominal (saldo)')
    setLocale('en')
  })
})

const row = (over: object = {}) => ({
  id: 1, head_transfer_id: 1, batch_id: 1, kind: 'ITEM', is_return: false, scope: 'RESERVATION', reason: 'company pays', business_date: '2026-09-30', created_at: '2026-09-30T13:00:00Z', by: 'Rina', approved_by: 'Boss',
  from: { id: 3, number: 'FOL000001' }, to: { id: 8, number: 'FOL000002', room: '101' }, origin: { id: 3, number: 'FOL000001' }, previous: { id: 3, number: 'FOL000001' }, head_previous: { id: 3, number: 'FOL000001' },
  amount: '122100', description: 'Minibar', charge_code: 'MINIBAR', original_item_id: 11, in_item_id: 12, out_item_id: 13, returns_transfer_id: null, returned_by_transfer_id: null,
  current: { id: 8, number: 'FOL000002' }, returnable: true, ...over,
}) as never

describe('ReturnDialog', () => {
  beforeEach(() => {
    setLocale('en')
    POST = vi.fn().mockResolvedValue({ data: { transfers: [{}], batch_id: 1, balances: [], kind: 'ITEM', scope: 'RETURN', replayed: false } })
  })
  afterEach(() => {
    mounted.splice(0).forEach((w) => w.unmount())
    document.body.innerHTML = ''
  })

  it('sends one or many back, to the previous folio or to the origin, with a reason and an approval', async () => {
    const w = mount(ReturnDialog, { props: { holderId: 8, holderNumber: 'FOL000002', rows: [row(), row({ id: 2, head_transfer_id: 2, amount: '50000' })] }, ...setupAuth() })
    mounted.push(w)
    await flushPromises()
    expect(body('[data-testid=return-total]').text()).toBe('Total: 172,100')
    expect(body('[data-testid=return-row-1]').text()).toContain('FOL000001')
    await type('input[name=return_reason]', 'wrong folio')
    ;(document.body.querySelector('input[value=ORIGIN]') as HTMLInputElement).click()
    await flushPromises()
    ;(document.body.querySelector('[data-testid=return-form]') as HTMLFormElement).dispatchEvent(new Event('submit', { cancelable: true }))
    await flushPromises()
    await approveWith('boss@hotel.test', 'secret')
    const call = POST.mock.calls[0]
    expect(call?.[0]).toBe('/api/v1/properties/{propertyId}/folios/{id}/transfers/return')
    expect(call?.[1].params.path).toEqual({ propertyId: 7, id: 8 })
    expect(call?.[1].body).toEqual({ transfer_ids: [1, 2], to: 'ORIGIN', reason: 'wrong folio', approval: { email: 'boss@hotel.test', password: 'secret' } })
    expect(w.emitted('done')?.[0]?.[0]).toBe('1 transfers sent back.')
  })

  it('offers a balance only the way back to the folio it left', async () => {
    const w = mount(ReturnDialog, { props: { holderId: 8, holderNumber: 'FOL000002', rows: [row({ kind: 'BALANCE', original_item_id: null })] }, ...setupAuth() })
    mounted.push(w)
    await flushPromises()
    expect((document.body.querySelector('input[value=ORIGIN]') as HTMLInputElement).disabled).toBe(true)
    expect(has('[data-testid=return-balance-note]')).toBe(true)
  })

  it('names a balance in words of the page, never with the description the database holds', async () => {
    const w = mount(ReturnDialog, { props: { holderId: 8, holderNumber: 'FOL000002', rows: [row({ kind: 'BALANCE', original_item_id: null, description: 'Transfer from FOL000010' })] }, ...setupAuth() })
    mounted.push(w)
    await flushPromises()
    expect(body('[data-testid=return-row-1]').text()).not.toContain('Transfer from')
    expect(body('[data-testid=return-row-1]').text()).toContain('Balance')
    w.unmount()
    setLocale('id')
    const id = mount(ReturnDialog, { props: { holderId: 8, holderNumber: 'FOL000002', rows: [row({ kind: 'BALANCE', original_item_id: null, description: 'Transfer from FOL000010' })] }, ...setupAuth() })
    mounted.push(id)
    await flushPromises()
    expect(body('[data-testid=return-row-1]').text()).toContain('Saldo')
    expect(body('[data-testid=return-row-1]').text()).not.toContain('Transfer from')
  })

  it('names the closed folio and lets the other way be chosen', async () => {
    POST = vi.fn().mockRejectedValue(new ApiError({ code: 'FOLIO_CLOSED', title: 'closed', status: 409, context: { folio_id: 3, folio_number: 'FOL000001' } } as never))
    const w = mount(ReturnDialog, { props: { holderId: 8, holderNumber: 'FOL000002', rows: [row()] }, ...setupAuth() })
    mounted.push(w)
    await flushPromises()
    await type('input[name=return_reason]', 'x')
    ;(document.body.querySelector('[data-testid=return-form]') as HTMLFormElement).dispatchEvent(new Event('submit', { cancelable: true }))
    await flushPromises()
    await approveWith('boss@hotel.test', 'secret')
    expect(body('[data-testid=return-closed]').text()).toContain('The folio FOL000001 is closed')
    expect(has('[data-testid=return-form]')).toBe(true)
  })
})

describe('TransferredList', () => {
  beforeEach(() => {
    setLocale('en')
    GET = vi.fn(async () => ({ data: { data: [row(), row({ id: 2, head_transfer_id: 5, returned_by_transfer_id: 9, returnable: false, to: { id: 9, number: 'FOL000003' }, current: { id: 9, number: 'FOL000003' } })] } }))
    POST = vi.fn().mockResolvedValue({ data: { transfers: [{}], batch_id: 1, balances: [], kind: 'ITEM', scope: 'RETURN', replayed: false } })
  })
  afterEach(() => {
    mounted.splice(0).forEach((w) => w.unmount())
    document.body.innerHTML = ''
  })

  it('lists what left the folio: the transaction, the amount, the folio it went to and where it is now, and who moved it', async () => {
    const w = mount(TransferredList, { props: { folioId: 3, folioNumber: 'FOL000001' }, ...setupAuth() })
    mounted.push(w)
    await flushPromises()
    expect(GET.mock.calls[0]?.[1].params.query).toEqual({ direction: 'out' })
    expect(w.get('[data-testid=transferred-1]').text()).toContain('Minibar')
    expect(w.get('[data-testid=transferred-amount-1]').text()).toBe('122,100')
    expect(w.get('[data-testid=transferred-to-1]').attributes('href')).toBe('/folios/8')
    expect(w.get('[data-testid=transferred-1]').text()).toContain('by Rina')
    expect(w.get('[data-testid=transferred-status-2]').text()).toBe('returned')
    expect(w.find('[data-testid=return-2]').exists()).toBe(false) // what was returned is not returned again
  })

  it('returns one line, or several that one folio holds, from the same dialog', async () => {
    const w = mount(TransferredList, { props: { folioId: 3, folioNumber: 'FOL000001' }, ...setupAuth() })
    mounted.push(w)
    await flushPromises()
    await w.get('[data-testid=return-1]').trigger('click')
    await flushPromises()
    expect(has('[data-testid=return-dialog]')).toBe(true)
    expect(body('[data-testid=return-row-1]').exists()).toBe(true)
  })

  it('offers no return to who may not reverse', async () => {
    const w = mount(TransferredList, { props: { folioId: 3, folioNumber: 'FOL000001' }, ...setupAuth(['folio.read']) })
    mounted.push(w)
    await flushPromises()
    expect(w.find('[data-testid=return-1]').exists()).toBe(false)
    expect(w.find('[data-testid=pick-transfer-1]').exists()).toBe(false)
  })
})
