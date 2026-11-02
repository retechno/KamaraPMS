import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { setLocale } from '@/i18n'
import BanquetScheduleDialog from './BanquetScheduleDialog.vue'
import BanquetSessionDialog from './BanquetSessionDialog.vue'

let POST = vi.fn()
let PUT = vi.fn()
let PATCH = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: vi.fn(async () => ({ data: {} })), POST: (...a: unknown[]) => POST(...a), PUT: (...a: unknown[]) => PUT(...a), PATCH: (...a: unknown[]) => PATCH(...a) } }))

const mounted: VueWrapper[] = []
const TEXT = 'input[data-slot=date-input-text]'

/** The dialogs of the banquet that hold a date: what is seen is what is sent. */
describe('the dates in the dialogs of the banquet', () => {
  beforeEach(async () => {
    setActivePinia(createPinia())
    POST = vi.fn(async () => ({ data: {} }))
    PUT = vi.fn(async () => ({ data: {} }))
    PATCH = vi.fn(async () => ({ data: {} }))
    await setLocale('en')
  })
  afterEach(() => {
    while (mounted.length) mounted.pop()!.unmount()
    document.body.innerHTML = ''
  })

  it('does not save the deposit schedule while a due date is half typed, and says why', async () => {
    const schedule = [{ label: 'First', due_date: '2026-11-10', percent: '30', amount: '0' }]
    const w = mount(BanquetScheduleDialog, { props: { open: false, propertyId: 7, eventId: 20, schedule: schedule as never }, attachTo: document.body })
    mounted.push(w)
    await w.setProps({ open: true })
    await flushPromises()
    const due = document.body.querySelector(`[data-testid=schedule-row-0] ${TEXT}`) as HTMLInputElement
    due.value = '3/2'
    due.dispatchEvent(new Event('input', { bubbles: true }))
    document.body.querySelector('[data-testid=schedule-dialog] form')!.dispatchEvent(new Event('submit', { cancelable: true, bubbles: true }))
    await flushPromises()
    expect(PUT).not.toHaveBeenCalled()
    expect(document.body.querySelector('[data-testid=schedule-dialog]')!.textContent).toContain('Write as dd/mm/yyyy')
  })

  it('does not save a session while its date is half typed, and says why', async () => {
    const event = { id: 20, status: 'INQUIRY', sessions: [], version: 1 }
    const w = mount(BanquetSessionDialog, { props: { open: false, propertyId: 7, event: event as never, rooms: [], prefill: { date: '2026-11-05' } }, attachTo: document.body })
    mounted.push(w)
    await w.setProps({ open: true })
    await flushPromises()
    const day = document.body.querySelector(`[data-testid=banquet-session-dialog] ${TEXT}`) as HTMLInputElement
    expect(day.value).toBe('05/11/2026')
    day.value = '3/2'
    day.dispatchEvent(new Event('input', { bubbles: true }))
    document.body.querySelector('[data-testid=banquet-session-dialog] form')!.dispatchEvent(new Event('submit', { cancelable: true, bubbles: true }))
    await flushPromises()
    expect(POST).not.toHaveBeenCalled()
    expect(PATCH).not.toHaveBeenCalled()
    expect(document.body.querySelector('[data-testid=banquet-session-dialog]')!.textContent).toContain('Write as dd/mm/yyyy')
  })
})
