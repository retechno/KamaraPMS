import { toApiError } from '@/api/problem'
import { authFetch } from '@/api/session'

/**
 * Opens a PDF document in a new tab. The documents need the access token, which only lives in memory, so a plain
 * link cannot fetch them: the file is fetched with the token and shown from a blob URL. The tab is opened first,
 * while the click is still the user's, or pop-up blockers would refuse it after the wait.
 */
export async function openPdf(path: string): Promise<void> {
  const tab = window.open('', '_blank')
  try {
    const res = await authFetch(new Request(new URL(path, window.location.origin).href, { headers: { Accept: 'application/pdf' } }))
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

/** The path of a document of a property. */
export const documentPath = {
  invoice: (propertyId: number, folioId: number) => `/api/v1/properties/${propertyId}/folios/${folioId}/invoice.pdf`,
  registrationCard: (propertyId: number, stayId: number) => `/api/v1/properties/${propertyId}/stays/${stayId}/registration-card.pdf`,
  receipt: (propertyId: number, paymentId: number) => `/api/v1/properties/${propertyId}/payments/${paymentId}/receipt.pdf`,
  confirmation: (propertyId: number, reservationId: number) => `/api/v1/properties/${propertyId}/reservations/${reservationId}/confirmation.pdf`,
  companyStatement: (propertyId: number, companyId: number, from?: string, to?: string) => {
    const q = new URLSearchParams()
    if (from) q.set('from', from)
    if (to) q.set('to', to)
    const qs = q.toString()
    return `/api/v1/properties/${propertyId}/companies/${companyId}/statement.pdf${qs ? `?${qs}` : ''}`
  },
}
