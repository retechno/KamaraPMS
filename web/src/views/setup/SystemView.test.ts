import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import SystemView from './SystemView.vue'

let GET = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...args: unknown[]) => GET(...args) } }))

const healthOnly = async (path: string) => ({ data: { status: 'ok', path } })
const problem = (status: number, code: string) => new ApiError({ type: 't', title: 'T', status, code, detail: 'd', request_id: 'req-1' })

function mountPage() {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'a@hotel.com', is_tenant_admin: true }, properties: [] } as never
  return mount(SystemView, { global: { plugins: [pinia] } })
}

describe('SystemView', () => {
  beforeEach(() => {
    GET = vi.fn()
    setLocale('en')
  })

  it('shows API and database as operational', async () => {
    GET.mockImplementation(healthOnly)
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.get('h1').text()).toBe('System')
    expect(GET).toHaveBeenCalledWith('/healthz')
    expect(GET).toHaveBeenCalledWith('/readyz')
    expect(wrapper.get('[data-testid=api-status]').text()).toBe('Operational')
    expect(wrapper.get('[data-testid=db-status]').text()).toBe('Operational')
    expect(wrapper.find('[role=alert]').exists()).toBe(false)
  })

  it('checks again with the refresh button', async () => {
    GET.mockImplementation(healthOnly)
    const wrapper = mountPage()
    await flushPromises()
    GET.mockClear()
    await wrapper.get('[data-testid=system-refresh]').trigger('click')
    await flushPromises()
    expect(GET).toHaveBeenCalledWith('/healthz')
  })

  it('reports a database outage with its error code', async () => {
    GET.mockImplementation(async (path: string) => {
      if (path === '/healthz') return { data: { status: 'ok' } }
      throw problem(503, 'NOT_READY')
    })
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.get('[data-testid=api-status]').text()).toBe('Operational')
    expect(wrapper.get('[data-testid=db-status]').text()).toBe('Unavailable')
    expect(wrapper.get('[role=alert]').text()).toContain('NOT_READY')
    expect(wrapper.get('[role=alert]').text()).toContain('req-1')
  })

  it('reports an unreachable API without guessing the database state', async () => {
    GET.mockRejectedValue(new TypeError('Failed to fetch'))
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.get('[data-testid=api-status]').text()).toBe('Unavailable')
    expect(wrapper.get('[data-testid=db-status]').text()).toBe('Unknown')
    expect(wrapper.get('[role=alert]').text()).toContain('NETWORK_ERROR')
  })

  it('says "cannot be checked", not "unavailable", when the answer is not the one of a health check', async () => {
    GET.mockRejectedValue(problem(404, 'NOT_FOUND')) // a proxy that does not pass /healthz on
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.get('[data-testid=api-status]').text()).toBe('Cannot be checked')
    expect(wrapper.get('[data-testid=db-status]').text()).toBe('Unknown')
    setLocale('id')
    await flushPromises()
    expect(wrapper.get('[data-testid=api-status]').text()).toBe('Tidak bisa diperiksa')
  })

  it('says "cannot be checked" for the database when only /readyz is not passed on', async () => {
    GET.mockImplementation(async (path: string) => {
      if (path === '/healthz') return { data: { status: 'ok' } }
      throw problem(404, 'NOT_FOUND')
    })
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.get('[data-testid=api-status]').text()).toBe('Operational')
    expect(wrapper.get('[data-testid=db-status]').text()).toBe('Cannot be checked')
  })

  it('says it is down when the proxy answers 502', async () => {
    GET.mockRejectedValue(problem(502, 'BAD_GATEWAY'))
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.get('[data-testid=api-status]').text()).toBe('Unavailable')
  })
})
