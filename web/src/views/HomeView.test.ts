import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { createMemoryHistory, createRouter } from 'vue-router'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import HomeView from './HomeView.vue'

// A fresh mock per test (reassigned in beforeEach); the module mock delegates to it.
let GET = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...args: unknown[]) => GET(...args) } }))

// The health endpoints answer ok; the manager dashboard (which an administrator also loads) has nothing to show here.
const okExceptDashboard = async (path: string) => ({ data: path.endsWith('/dashboard') ? null : { status: 'ok' } })

function mountView(admin = true) {
  const pinia = createPinia()
  setActivePinia(pinia)
  // The status of the system is for the administrator.
  useAuthStore().me = { user: { id: 5, email: 'a@hotel.com', is_tenant_admin: admin }, properties: [] } as never
  return mount(HomeView, { global: { plugins: [pinia] } })
}

describe('HomeView system status', () => {
  beforeEach(() => {
    GET = vi.fn()
  })

  it('shows no system status, and makes no health check, to staff who are not administrators', async () => {
    GET.mockImplementation(okExceptDashboard)
    const wrapper = mountView(false)
    await flushPromises()
    expect(wrapper.find('[data-testid=system-status-card]').exists()).toBe(false)
    expect(GET).not.toHaveBeenCalledWith('/healthz')
  })

  it('shows API and database as operational', async () => {
    GET.mockImplementation(okExceptDashboard)
    const wrapper = mountView()
    await flushPromises()
    expect(GET).toHaveBeenCalledWith('/healthz')
    expect(GET).toHaveBeenCalledWith('/readyz')
    expect(wrapper.get('[data-testid=api-status]').text()).toBe('Operational')
    expect(wrapper.get('[data-testid=db-status]').text()).toBe('Operational')
    expect(wrapper.find('[role=alert]').exists()).toBe(false)
  })

  it('reports a database outage with its error code', async () => {
    GET.mockImplementation(async (path: string) => {
      if (path === '/healthz') return { data: { status: 'ok' } }
      throw new ApiError({
        type: 't',
        title: 'Service Unavailable',
        status: 503,
        code: 'NOT_READY',
        detail: 'the database is not reachable',
        request_id: 'req-1',
      })
    })
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.get('[data-testid=api-status]').text()).toBe('Operational')
    expect(wrapper.get('[data-testid=db-status]').text()).toBe('Unavailable')
    expect(wrapper.get('[role=alert]').text()).toContain('NOT_READY')
    expect(wrapper.get('[role=alert]').text()).toContain('req-1')
  })

  it('reports an unreachable API without guessing the database state', async () => {
    GET.mockRejectedValue(new TypeError('Failed to fetch'))
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.get('[data-testid=api-status]').text()).toBe('Unavailable')
    expect(wrapper.get('[data-testid=db-status]').text()).toBe('Unknown')
    expect(wrapper.get('[role=alert]').text()).toContain('NETWORK_ERROR')
  })

  async function mountWith(permissions: string[], clock: object | null = { business_date: '2026-09-30', property_local_time: '2026-09-30T20:00:00+07:00', timezone: 'Asia/Jakarta', night_audit_allowed: false, night_audit_overdue: false }, admin = false) {
    GET.mockImplementation(okExceptDashboard)
    const pinia = createPinia()
    setActivePinia(pinia)
    useAuthStore().me = { user: { id: 1, email: 'a@b.c', is_tenant_admin: admin }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
    const property = usePropertyStore()
    property.loaded = true
    property.properties = [{ id: 7, code: 'BALI', name: 'Bali' }] as never
    property.currentId = 7
    property.current = { id: 7, name: 'Bali Resort', night_audit_earliest_time: '20:00' } as never
    property.clock = clock as never
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
    const w = mount(HomeView, { global: { plugins: [pinia, router] } })
    await flushPromises()
    return w
  }

  it('offers the quick actions the role may use', async () => {
    const all = await mountWith(['reservation.create', 'frontdesk.checkin', 'nightaudit.run'])
    expect(all.get('[data-testid=quick-new-reservation]').attributes('href')).toBe('/reservations/new')
    expect(all.get('[data-testid=quick-walk-in]').attributes('href')).toBe('/walk-in')
    expect(all.get('[data-testid=quick-night-audit]').attributes('href')).toBe('/night-audit')
    const none = await mountWith(['guest.read'])
    expect(none.find('[data-testid=quick-new-reservation]').exists()).toBe(false)
    expect(none.find('[data-testid=quick-walk-in]').exists()).toBe(false)
    expect(none.find('[data-testid=quick-night-audit]').exists()).toBe(false)
  })

  it('shows the property and the business day', async () => {
    const w = await mountWith([])
    expect(w.get('[data-testid=property-name]').text()).toBe('Bali Resort')
    const card = w.get('[data-testid=business-day-card]').text()
    expect(card).toContain('30 Sep 2026')
    expect(card).toContain('Allowed from 20:00 on 30 Sep 2026')
    const ready = await mountWith([], { business_date: '2026-09-30', property_local_time: '2026-09-30T21:00:00+07:00', timezone: 'Asia/Jakarta', night_audit_allowed: true, night_audit_overdue: false })
    expect(ready.get('[data-testid=business-day-card]').text()).toContain('Can run now')
  })

  it('shows the system status in Indonesian', async () => {
    setLocale('id')
    const w = await mountWith([], undefined, true)
    expect(w.get('[data-testid=api-status]').text()).toBe('Berjalan')
    expect(w.text()).toContain('Status sistem')
    setLocale('en')
  })
})
