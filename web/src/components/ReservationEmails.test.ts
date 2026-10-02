import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import ReservationEmails from './ReservationEmails.vue'

let GET = vi.fn()
let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a) } }))

const sent = { id: 2, kind: 'RESERVATION_CONFIRMATION', to: 'siti@example.test', status: 'SENT', attempts: 1, sent_at: '2026-09-30T13:00:00Z', created_at: '2026-09-30T12:59:00Z' }

function mountPanel(state: object, permissions = ['reservation.read', 'reservation.update'], confirmed = true) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn().mockResolvedValue({ data: state })
  POST = vi.fn().mockResolvedValue({ data: {} })
  return mount(ReservationEmails, { props: { reservationId: 9, confirmed }, global: { plugins: [pinia] } })
}

describe('ReservationEmails', () => {
  beforeEach(() => {
    GET = vi.fn()
    POST = vi.fn()
  })

  it('lists the e-mails with their state and offers to send again', async () => {
    const w = mountPanel({ enabled: true, data: [sent, { ...sent, id: 3, status: 'QUEUED', sent_at: null, attempts: 2, last_error: 'connection refused' }] })
    await flushPromises()
    expect(GET.mock.calls[0]).toEqual(['/api/v1/properties/{propertyId}/reservations/{id}/emails', { params: { path: { propertyId: 7, id: 9 } } }])
    expect(w.get('[data-testid=email-2]').text()).toContain('Sent')
    expect(w.get('[data-testid=email-3]').text()).toContain('Waiting to be sent')
    expect(w.get('[data-testid=email-3]').text()).toContain('connection refused')
    expect(w.get('[data-testid=resend]').attributes('disabled')).toBeDefined() // one is still waiting
  })

  it('queues another confirmation and reloads', async () => {
    const w = mountPanel({ enabled: true, data: [sent] })
    await flushPromises()
    await w.get('[data-testid=resend]').trigger('click')
    await flushPromises()
    expect(POST.mock.calls[0]).toEqual(['/api/v1/properties/{propertyId}/reservations/{id}/emails', { params: { path: { propertyId: 7, id: 9 } } }])
    expect(w.get('[data-testid=emails-notice]').text()).toContain('queued')
    expect(GET.mock.calls.length).toBe(2)
  })

  it('shows the refusal, for example a booker without an address', async () => {
    const w = mountPanel({ enabled: true, data: [] })
    await flushPromises()
    expect(w.find('[data-testid=emails-none]').exists()).toBe(true)
    POST.mockRejectedValue(new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'GUEST_HAS_NO_EMAIL', detail: 'no address' }))
    await w.get('[data-testid=resend]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid=emails-error]').text()).toContain('GUEST_HAS_NO_EMAIL')
  })

  it('says when e-mail is off and hides the button without permission or for a cancelled reservation', async () => {
    const off = mountPanel({ enabled: false, data: [] })
    await flushPromises()
    expect(off.get('[data-testid=emails-off]').text()).toContain('PMS_SMTP_HOST')
    expect(off.find('[data-testid=resend]').exists()).toBe(false)
    const reader = mountPanel({ enabled: true, data: [] }, ['reservation.read'])
    await flushPromises()
    expect(reader.find('[data-testid=resend]').exists()).toBe(false)
    const cancelled = mountPanel({ enabled: true, data: [] }, ['reservation.read', 'reservation.update'], false)
    await flushPromises()
    expect(cancelled.find('[data-testid=resend]').exists()).toBe(false)
  })
})
