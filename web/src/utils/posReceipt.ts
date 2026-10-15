import type { PosBill } from '@/api/types'
import { formatDateTime, formatMoney } from '@/utils/format'

const esc = (s: string): string => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;')

/**
 * The receipt, or the bill before it is paid, as a page the browser prints (docs/architecture/34-pos.md 11: PDF or browser, no agent). The amounts are the bill's, as the server wrote them; nothing is
 * computed here. An open bill says "not a receipt" in the language of the page.
 */
export function receiptHtml(bill: PosBill, tr: (key: string, params?: Record<string, unknown>) => string): string {
  const rows = bill.lines
    .filter((l) => l.status !== 'VOID')
    .map((l) => {
      const opts = l.options.map((o) => o.name).join(', ')
      return `<tr><td>${esc(String(Number(l.quantity)))} × ${esc(l.product_name)}${opts ? `<br><small>${esc(opts)}</small>` : ''}</td><td class="n">${esc(formatMoney(l.total))}</td></tr>`
    })
    .join('')
  const open = bill.status === 'OPEN'
  const totals = bill.totals
  const line = (label: string, value: string, strong = false): string => `<tr class="${strong ? 'b' : ''}"><td>${esc(label)}</td><td class="n">${esc(value)}</td></tr>`
  const pays = bill.payments
    .filter((p) => p.status === 'POSTED')
    .map((p) => line(tr(`posPay.method_${p.method === 'CITY_LEDGER' ? 'COMPANY' : p.method}`), formatMoney(p.amount)))
    .join('')
  return `<!doctype html><html><head><meta charset="utf-8"><title>${esc(bill.bill_number)}</title>
<style>
  body { font: 12px/1.35 ui-monospace, Menlo, Consolas, monospace; margin: 0; padding: 8px; max-width: 320px; }
  h1 { font-size: 15px; margin: 0; text-align: center; }
  p { margin: 2px 0; text-align: center; }
  table { width: 100%; border-collapse: collapse; margin: 6px 0; }
  td { padding: 2px 0; vertical-align: top; }
  td.n { text-align: right; white-space: nowrap; padding-left: 8px; }
  tr.b td { font-weight: 700; border-top: 1px dashed #000; padding-top: 4px; }
  hr { border: 0; border-top: 1px dashed #000; }
</style></head><body>
<h1>${esc(bill.outlet_name)}</h1>
<p>${esc(bill.bill_number)}${bill.table_code ? ` · ${esc(tr('posFloor.table', { code: bill.table_code }))}` : ''}</p>
<p>${esc(formatDateTime(bill.closed_at ?? bill.opened_at))}${bill.waiter_name ? ` · ${esc(bill.waiter_name)}` : ''}</p>
${open ? `<p><strong>${esc(tr('posBill.notReceipt'))}</strong></p>` : ''}
<hr><table>${rows}</table><hr>
<table>
${line(tr('posBill.net'), formatMoney(totals.net))}
${line(tr('posBill.service'), formatMoney(totals.service))}
${line(tr('posBill.tax'), formatMoney(totals.tax))}
${Number(totals.rounding) !== 0 ? line(tr('posBill.rounding'), formatMoney(totals.rounding)) : ''}
${line(open ? tr('posBill.toPay') : tr('posBill.total'), formatMoney(open ? totals.payable : totals.sold), true)}
${pays}
</table>
${bill.room_charges.some((c) => !c.returned_at) ? `<p>${esc(tr('posBill.signRoom'))}</p><p><br>______________________</p>` : ''}
<p>${esc(tr('posBill.thanks'))}</p>
</body></html>`
}

/** Opens the receipt in a window of its own and asks the browser to print it. */
export function printReceipt(bill: PosBill, tr: (key: string, params?: Record<string, unknown>) => string): void {
  const w = window.open('', '_blank', 'width=380,height=640')
  if (!w) return
  w.document.open()
  w.document.write(receiptHtml(bill, tr))
  w.document.close()
  w.focus()
  w.print()
}
