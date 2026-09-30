import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import HomeView from './HomeView.vue'

// A fresh mock per test (reassigned in beforeEach); the module mock delegates to it.
let GET = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...args: unknown[]) => GET(...args) } }))

function mountView() {
  const pinia = createPinia()
  setActivePinia(pinia)
  return mount(HomeView, { global: { plugins: [pinia] } })
}

describe('HomeView system status', () => {
  beforeEach(() => {
    GET = vi.fn()
  })

  it('shows API and database as operational', async () => {
    GET.mockResolvedValue({ data: { status: 'ok' } })
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
})
