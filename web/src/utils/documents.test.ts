import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { documentPath, openPdf } from './documents'

let authFetch = vi.fn()
vi.mock('@/api/session', () => ({ authFetch: (...a: unknown[]) => authFetch(...a) }))

describe('openPdf', () => {
  const tab = { location: { href: '' }, close: vi.fn() }

  beforeEach(() => {
    authFetch = vi.fn()
    tab.location.href = ''
    tab.close = vi.fn()
    vi.stubGlobal('open', vi.fn().mockReturnValue(tab))
    URL.createObjectURL = vi.fn().mockReturnValue('blob:doc')
    URL.revokeObjectURL = vi.fn()
  })

  it('fetches the file with the session and shows it in the tab opened by the click', async () => {
    authFetch.mockResolvedValue(new Response('%PDF-1.3', { status: 200, headers: { 'Content-Type': 'application/pdf' } }))
    await openPdf(documentPath.invoice(7, 12))
    expect(window.open).toHaveBeenCalledWith('', '_blank')
    const req = authFetch.mock.calls[0]?.[0] as Request
    expect(new URL(req.url, 'http://x').pathname).toBe('/api/v1/properties/7/folios/12/invoice.pdf')
    expect(tab.location.href).toBe('blob:doc')
  })

  it('closes the tab and throws the problem when the server refuses', async () => {
    authFetch.mockResolvedValue(new Response(JSON.stringify({ type: 't', title: 'Forbidden', status: 403, code: 'PERMISSION_DENIED', detail: 'no' }), { status: 403 }))
    await expect(openPdf(documentPath.receipt(7, 3))).rejects.toMatchObject({ code: 'PERMISSION_DENIED' })
    expect(tab.close).toHaveBeenCalled()
    expect(tab.location.href).toBe('')
  })

  it('turns a non-problem failure into an ApiError and shows the file here when the tab was blocked', async () => {
    authFetch.mockResolvedValue(new Response('boom', { status: 502 }))
    await expect(openPdf('/x')).rejects.toBeInstanceOf(ApiError)
    vi.stubGlobal('open', vi.fn().mockReturnValue(null))
    authFetch.mockResolvedValue(new Response('%PDF', { status: 200 }))
    const loc = { href: '', origin: 'http://localhost' }
    vi.stubGlobal('location', loc)
    await openPdf('/x')
    expect(loc.href).toBe('blob:doc')
  })
})

describe('documentPath', () => {
  it('names the four documents', () => {
    expect(documentPath.registrationCard(7, 5)).toBe('/api/v1/properties/7/stays/5/registration-card.pdf')
    expect(documentPath.confirmation(7, 9)).toBe('/api/v1/properties/7/reservations/9/confirmation.pdf')
  })
})
