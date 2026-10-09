import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { toastText } from '@/test/toasts'
import { usePropertyStore } from '@/stores/property'
import EditRateDialog from './EditRateDialog.vue'

let GET = vi.fn()
let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a) } }))

const row = { id: 5, guest: { id: 3, name: 'Siti Nurhaliza' }, room: { id: 21, number: '101', room_type_code: 'DLX', room_type_name: 'Deluxe' }, rate: { rate_plan_code: 'BAR' } }
const night = (date: string, amount: string, status: string) => ({ date, amount, price_mode: 'EXCLUSIVE', is_override: false, posted: status !== 'OPEN', posted_on: status === 'OPEN' ? null : '2026-09-30', status })
const detail = () => ({ stay: { id: 5, version: 3 }, nightly_rates: [night('2026-09-30', '1000000', 'CLOSED'), night('2026-10-01', '1000000', 'OPEN'), night('2026-10-02', '1000000', 'OPEN')] })

let mounted: VueWrapper | null = null
const body = () => document.body

async function mountDialog(permissions = ['reservation.read', 'frontdesk.rate_change', 'folio.adjust']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn().mockResolvedValue({ data: detail() })
  POST = vi.fn().mockResolvedValue({ data: { stay: {}, changes: [] } })
  mounted = mount(EditRateDialog, { props: { open: true, row: row as never }, attachTo: document.body, global: { plugins: [pinia] } })
  await flushPromises()
  return mounted
}

async function type(selector: string, value: string): Promise<void> {
  const el = body().querySelector(selector) as HTMLInputElement
  el.value = value
  el.dispatchEvent(new Event('input'))
  await flushPromises()
}
const click = async (selector: string) => {
  ;(body().querySelector(selector) as HTMLElement).click()
  await flushPromises()
}
const submit = async () => {
  body().querySelector('form[data-testid=rate-form]')!.dispatchEvent(new Event('submit', { cancelable: true }))
  await flushPromises()
}

describe('EditRateDialog', () => {
  beforeEach(() => {
    setLocale('en')
  })
  afterEach(() => {
    mounted?.unmount()
    mounted = null
    document.body.innerHTML = ''
  })

  it('shows the room, guest, rate plan and each night with its status and current rate', async () => {
    await mountDialog()
    expect(GET.mock.calls[0]).toEqual(['/api/v1/properties/{propertyId}/stays/{id}', { params: { path: { propertyId: 7, id: 5 } } }])
    expect(body().querySelector('[data-testid=rate-room]')?.textContent).toContain('101')
    expect(body().querySelector('[data-testid=rate-guest]')?.textContent).toBe('Siti Nurhaliza')
    expect(body().querySelector('[data-testid=rate-plan]')?.textContent).toBe('BAR')
    expect(body().querySelector('[data-testid="night-2026-09-30"] [data-status]')?.getAttribute('data-status')).toBe('CLOSED')
    expect(body().querySelector('[data-testid="night-2026-10-01"] [data-status]')?.textContent).toBe('Open')
    expect(body().querySelector('[data-testid="current-2026-10-01"]')?.textContent).toBe('1,000,000')
  })

  it('needs a reason and a new rate before it applies', async () => {
    await mountDialog()
    expect((body().querySelector('[data-testid=rate-apply]') as HTMLButtonElement).disabled).toBe(true)
    await type('input[name=amount]', '800000')
    expect((body().querySelector('[data-testid=rate-apply]') as HTMLButtonElement).disabled).toBe(true) // no reason
    await type('input[name=reason]', 'negotiated')
    expect((body().querySelector('[data-testid=rate-apply]') as HTMLButtonElement).disabled).toBe(false)
    expect(body().querySelector('[data-testid="next-2026-10-01"]')?.textContent).toBe('800,000')
  })

  it('changes the selected open night without an approval', async () => {
    const w = await mountDialog()
    await type('input[name=amount]', '1100000')
    await type('input[name=reason]', 'negotiated')
    await submit()
    expect(POST.mock.calls[0]).toEqual(['/api/v1/properties/{propertyId}/stays/{id}/rates', {
      params: { path: { propertyId: 7, id: 5 } },
      body: { version: 3, apply_to: 'NIGHT', date: '2026-10-01', amount: '1100000', reason: 'negotiated', approval: undefined },
    }])
    expect(w.emitted('saved')).toHaveLength(1)
    expect(w.emitted('update:open')?.[0]).toEqual([false])
  })

  it('asks for the approval of a rate approver when the rate goes down, and not when it goes up', async () => {
    await mountDialog()
    await type('input[name=amount]', '1100000')
    expect(body().querySelector('[data-testid=lowered-note]')).toBeNull()
    await type('input[name=amount]', '800000')
    await type('input[name=reason]', 'competitor')
    expect(body().querySelector('[data-testid=lowered-note]')).not.toBeNull()
    await submit()
    expect(POST).not.toHaveBeenCalled()
    await type('input[name=approval_email]', 'boss@example.com')
    await type('input[name=approval_password]', 'secret')
    body().querySelector('[data-testid=approval-dialog]')!.dispatchEvent(new Event('submit', { cancelable: true }))
    await flushPromises()
    expect(POST.mock.calls[0]?.[1]).toMatchObject({ body: { amount: '800000', approval: { email: 'boss@example.com', password: 'secret' } } })
  })

  it('lets a rate approver lower the rate without credentials', async () => {
    await mountDialog(['reservation.read', 'frontdesk.rate_change', 'reservation.override_rate_approve'])
    await type('input[name=amount]', '800000')
    await type('input[name=reason]', 'competitor')
    expect(body().querySelector('[data-testid=lowered-note]')).toBeNull()
    await submit()
    expect(body().querySelector('[data-testid=approval-dialog]')).toBeNull()
    expect(POST.mock.calls[0]?.[1]).toMatchObject({ body: { amount: '800000', approval: undefined } })
  })

  it('applies to the remaining nights that are not charged, from the selected night', async () => {
    await mountDialog()
    await click('input[name=apply_to][value=REMAINING]')
    await type('input[name=amount]', '1300000')
    expect(body().querySelector('[data-testid="next-2026-10-01"]')?.textContent).toBe('1,300,000')
    expect(body().querySelector('[data-testid="next-2026-10-02"]')?.textContent).toBe('1,300,000')
    expect(body().querySelector('[data-testid="next-2026-09-30"]')?.textContent).toBe('—') // a charged night is not part of it
    await type('input[name=reason]', 'long stay')
    await submit()
    expect(POST.mock.calls[0]?.[1]).toMatchObject({ body: { apply_to: 'REMAINING', date: '2026-10-01', amount: '1300000' } })
  })

  it('asks for an approval to correct a charged night and sends it with the change', async () => {
    await mountDialog()
    await click('input[name=night][value="2026-09-30"]')
    await type('input[name=amount]', '1200000')
    await type('input[name=reason]', 'wrong rate')
    expect(body().querySelector('[data-testid=charged-note]')).not.toBeNull()
    await submit()
    expect(POST).not.toHaveBeenCalled()
    expect(body().querySelector('[data-testid=approval-dialog]')).not.toBeNull()
    await type('input[name=approval_email]', 'boss@example.com')
    await type('input[name=approval_password]', 'secret')
    body().querySelector('[data-testid=approval-dialog]')!.dispatchEvent(new Event('submit', { cancelable: true }))
    await flushPromises()
    expect(POST.mock.calls[0]?.[1]).toMatchObject({ body: { apply_to: 'NIGHT', date: '2026-09-30', amount: '1200000', approval: { email: 'boss@example.com', password: 'secret' } } })
  })

  it('does not offer a charged night to someone who may not adjust folios', async () => {
    await mountDialog(['reservation.read', 'frontdesk.rate_change'])
    const charged = body().querySelector('input[name=night][value="2026-09-30"]') as HTMLInputElement
    expect(charged.disabled).toBe(true)
    expect((body().querySelector('input[name=night][value="2026-10-01"]') as HTMLInputElement).disabled).toBe(false)
  })

  it('shows the server refusal and reloads the nights when the stay changed', async () => {
    await mountDialog()
    POST.mockRejectedValue(new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'VERSION_CONFLICT', detail: 'changed' }))
    await type('input[name=amount]', '1100000')
    await type('input[name=reason]', 'x')
    await submit()
    expect(toastText()).toContain('changed')
    expect(GET).toHaveBeenCalledTimes(2)
  })
})
