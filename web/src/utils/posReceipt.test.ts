import { describe, expect, it } from 'vitest'
import { receiptHtml } from './posReceipt'

const tr = (key: string, params?: Record<string, unknown>): string => (params ? `${key} ${JSON.stringify(params)}` : key)
const line = (over: object) => ({ id: 1, product_name: 'Nasi goreng', options: [], quantity: '2', status: 'SENT', total: '121000', net_amount: '100000', ...over })
const bill = (over: object = {}) => ({
  bill_number: 'R-0123', outlet_name: 'Restoran <Teratai>', table_code: 'T5', status: 'OPEN', opened_at: '2026-10-15T05:00:00Z', closed_at: null, waiter_name: 'Ketut', payments: [], room_charges: [],
  lines: [line({}), line({ id: 2, product_name: 'Steak', status: 'VOID', total: '181500', net_amount: '165000' })],
  totals: { net: '100000', service: '10000', tax: '11000', rounding: '0', payable: '121000', sold: '121000' }, ...over,
})

describe('the receipt', () => {
  it('prints the lines that were not voided and escapes what a person typed', () => {
    const html = receiptHtml(bill() as never, tr)
    expect(html).toContain('Restoran &lt;Teratai&gt;')
    expect(html).toContain('Nasi goreng')
    expect(html).not.toContain('Steak')
    expect(html).toContain('121,000')
  })

  it('prints a line at its menu price, before service and tax, so the lines add up to the net', () => {
    const html = receiptHtml(bill() as never, tr)
    const row = /<tr><td>[^<]*Nasi goreng.*?<\/tr>/s.exec(html)?.[0] ?? ''
    expect(row).toContain('100,000')
    expect(row).not.toContain('121,000')
  })

  it('says an open bill is not a receipt, and a paid one lists its payments', () => {
    expect(receiptHtml(bill() as never, tr)).toContain('posBill.notReceipt')
    const paid = receiptHtml(bill({ status: 'CLOSED', payments: [{ method: 'CASH', amount: '121000', status: 'POSTED' }, { method: 'CARD', amount: '5', status: 'VOIDED' }] }) as never, tr)
    expect(paid).not.toContain('posBill.notReceipt')
    expect(paid).toContain('posPay.method_CASH')
    expect(paid).not.toContain('posPay.method_CARD')
  })

  it('asks for a signature when the bill is charged to a room', () => {
    expect(receiptHtml(bill({ room_charges: [{ id: 1, amount: '1', returned_at: null }] }) as never, tr)).toContain('posBill.signRoom')
  })
})
