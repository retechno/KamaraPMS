import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { emitSubscriptionAccess } from '@/api/subscriptionEvents'
import { useAuthStore } from './auth'
import { useSubscriptionStore } from './subscription'

let GET = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...args: unknown[]) => GET(...args), POST: vi.fn() } }))

const summary = (over: object = {}) => ({
  plan_code: 'STD', plan_name: 'Standard', modules: ['CORE', 'POS'], status: 'ACTIVE', access: 'FULL', started_on: '2026-01-01', paid_through: '2026-12-31', grace_ends_on: null, days_left: 40,
  limits: { max_properties: null, max_rooms: 10, max_users: null }, usage: { properties: 1, rooms: 10, users: 2 }, is_trial_ending: false, billing_contact: 'billing@example.com', ...over,
})

describe('subscription store', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    GET = vi.fn(async () => ({ data: summary() }))
    setActivePinia(createPinia())
  })
  afterEach(() => vi.useRealTimers())

  it('keeps the whole summary and the plan modules', async () => {
    const s = useSubscriptionStore()
    expect(s.loaded).toBe(false)
    expect(s.has('POS')).toBe(true) // nothing is hidden until it is read
    await s.load()
    expect(s.loaded).toBe(true)
    expect(s.has('POS')).toBe(true)
    expect(s.has('BANQUET')).toBe(false)
    expect(s.has('CORE')).toBe(true)
    expect(s.access).toBe('FULL')
    expect(s.daysLeft).toBe(40)
    expect(s.paidThrough).toBe('2026-12-31')
    expect(s.billingContact).toBe('billing@example.com')
    expect(s.isTrialEnding).toBe(false)
    await s.load() // a second read is not asked again
    expect(GET).toHaveBeenCalledTimes(1)
  })

  it('is emptied by reset and by signing out', async () => {
    const s = useSubscriptionStore()
    await s.load()
    s.reset()
    expect(s.loaded).toBe(false)
    expect(s.summary).toBeNull()
    await s.load()
    useAuthStore().signedOut()
    expect(s.summary).toBeNull()
    expect(s.modules).toBeNull()
  })

  it('reloads once when a response reports another access state', async () => {
    const s = useSubscriptionStore()
    await s.load()
    const stop = s.watchAccess()
    GET.mockImplementation(async () => ({ data: summary({ access: 'GRACE', days_left: 9 }) }))
    emitSubscriptionAccess('FULL', false) // the same state: nothing
    emitSubscriptionAccess(null, false) // no header (a public route): nothing
    await vi.advanceTimersByTimeAsync(1500)
    expect(GET).toHaveBeenCalledTimes(1)

    emitSubscriptionAccess('GRACE', false)
    emitSubscriptionAccess('GRACE', false)
    emitSubscriptionAccess('GRACE', false) // three responses, one reload
    await vi.advanceTimersByTimeAsync(1500)
    expect(GET).toHaveBeenCalledTimes(2)
    expect(s.access).toBe('GRACE')
    stop()
  })

  it('reloads after a write refused as read only, even if the header is as held', async () => {
    const s = useSubscriptionStore()
    await s.load()
    const stop = s.watchAccess()
    GET.mockImplementation(async () => ({ data: summary({ access: 'READ_ONLY', days_left: 0 }) }))
    emitSubscriptionAccess('FULL', true)
    await vi.advanceTimersByTimeAsync(1500)
    expect(s.access).toBe('READ_ONLY')
    stop()
  })

  it('does nothing before the first read, and nothing after it stopped listening', async () => {
    const s = useSubscriptionStore()
    const stop = s.watchAccess()
    emitSubscriptionAccess('READ_ONLY', false)
    await vi.advanceTimersByTimeAsync(1500)
    expect(GET).not.toHaveBeenCalled()
    await s.load()
    stop()
    emitSubscriptionAccess('READ_ONLY', false)
    await vi.advanceTimersByTimeAsync(1500)
    expect(GET).toHaveBeenCalledTimes(1)
  })
})
