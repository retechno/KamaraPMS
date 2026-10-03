import { toApiError } from '@/api/problem'
import { authFetch } from '@/api/session'
import { currentLocale } from '@/i18n'

/**
 * Opens a PDF document in a new tab. The documents need the access token, which only lives in memory, so a plain
 * link cannot fetch them: the file is fetched with the token and shown from a blob URL. The tab is opened first,
 * while the click is still the user's, or pop-up blockers would refuse it after the wait.
 */
export async function openPdf(path: string): Promise<void> {
  const tab = window.open('', '_blank')
  try {
    // the document is written in the language of the page
    const target = new URL(path, window.location.origin)
    target.searchParams.set('lang', currentLocale())
    const res = await authFetch(new Request(target.href, { headers: { Accept: 'application/pdf' } }))
    if (!res.ok) {
      let body: unknown
      try {
        body = await res.json()
      } catch {
        body = undefined
      }
      throw toApiError(body, res.status)
    }
    const url = URL.createObjectURL(new Blob([await res.arrayBuffer()], { type: 'application/pdf' }))
    if (tab) {
      tab.location.href = url
    } else {
      window.location.href = url // the browser refused the tab: show it here rather than lose it
    }
    // The tab has taken over the URL by now; releasing it later keeps the file open while it is being read.
    window.setTimeout(() => URL.revokeObjectURL(url), 5 * 60 * 1000)
  } catch (e) {
    tab?.close()
    throw e
  }
}

/**
 * Downloads a file a POST produces (an export): the body goes as JSON, the answer is saved under the name the server gives. The headers
 * `X-Export-Invoices` and `X-Export-Sha256` tell what was written.
 */
export async function downloadExport(path: string, body: unknown): Promise<{ invoices: string | null; sha256: string | null }> {
  const res = await authFetch(new Request(new URL(path, window.location.origin).href, { method: 'POST', headers: { 'Content-Type': 'application/json', Accept: 'text/csv' }, body: JSON.stringify(body) }))
  if (!res.ok) {
    let problem: unknown
    try {
      problem = await res.json()
    } catch {
      problem = undefined
    }
    throw toApiError(problem, res.status)
  }
  const name = /filename="([^"]+)"/.exec(res.headers.get('Content-Disposition') ?? '')?.[1] ?? 'export.csv'
  const url = URL.createObjectURL(new Blob([await res.arrayBuffer()], { type: 'text/csv' }))
  const a = document.createElement('a')
  a.href = url
  a.download = name
  document.body.appendChild(a)
  a.click()
  a.remove()
  window.setTimeout(() => URL.revokeObjectURL(url), 60 * 1000)
  return { invoices: res.headers.get('X-Export-Invoices'), sha256: res.headers.get('X-Export-Sha256') }
}

/** The path of a document of a property. */
export const documentPath = {
  invoice: (propertyId: number, folioId: number) => `/api/v1/properties/${propertyId}/folios/${folioId}/invoice.pdf`,
  registrationCard: (propertyId: number, stayId: number) => `/api/v1/properties/${propertyId}/stays/${stayId}/registration-card.pdf`,
  receipt: (propertyId: number, paymentId: number) => `/api/v1/properties/${propertyId}/payments/${paymentId}/receipt.pdf`,
  confirmation: (propertyId: number, reservationId: number) => `/api/v1/properties/${propertyId}/reservations/${reservationId}/confirmation.pdf`,
  /** The accounting reports as PDF: the same parameters as the JSON reports (`from`, `to`, `as_of`). */
  accounting: (propertyId: number, report: 'trial-balance' | 'income-statement' | 'balance-sheet', query: Record<string, string | undefined> = {}) => {
    const q = new URLSearchParams()
    for (const [k, v] of Object.entries(query)) if (v) q.set(k, v)
    const qs = q.toString()
    return `/api/v1/properties/${propertyId}/accounting/${report}.pdf${qs ? `?${qs}` : ''}`
  },
  ledger: (propertyId: number, accountId: number, query: Record<string, string | undefined> = {}) => {
    const q = new URLSearchParams()
    for (const [k, v] of Object.entries(query)) if (v) q.set(k, v)
    const qs = q.toString()
    return `/api/v1/properties/${propertyId}/accounting/accounts/${accountId}/ledger.pdf${qs ? `?${qs}` : ''}`
  },
  taxInvoice: (propertyId: number, invoiceId: number) => `/api/v1/properties/${propertyId}/tax/invoices/${invoiceId}/invoice.pdf`,
  taxReturn: (propertyId: number, returnId: number) => `/api/v1/properties/${propertyId}/tax/returns/${returnId}/return.pdf`,
  taxWorksheet: (propertyId: number, taxId: number, period: string) => `/api/v1/properties/${propertyId}/tax/worksheet.pdf?tax_id=${taxId}&period=${period}`,
  creditNote: (propertyId: number, adjustmentId: number) => `/api/v1/properties/${propertyId}/city-ledger/adjustments/${adjustmentId}/credit-note.pdf`,
  companyInvoice: (propertyId: number, invoiceId: number) => `/api/v1/properties/${propertyId}/city-ledger/invoices/${invoiceId}/invoice.pdf`,
  companyStatement: (propertyId: number, companyId: number, from?: string, to?: string) => {
    const q = new URLSearchParams()
    if (from) q.set('from', from)
    if (to) q.set('to', to)
    const qs = q.toString()
    return `/api/v1/properties/${propertyId}/companies/${companyId}/statement.pdf${qs ? `?${qs}` : ''}`
  },
}
