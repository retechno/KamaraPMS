import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { setLocale } from '@/i18n'
import { usePropertyStore } from '@/stores/property'
import HousekeeperToday from './HousekeeperToday.vue'

let GET = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a) } }))

async function mountCard() {
  const pinia = createPinia()
  setActivePinia(pinia)
  usePropertyStore().currentId = 7
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  const w = mount(HousekeeperToday, { global: { plugins: [pinia, router] } })
  await flushPromises()
  return w
}

describe('HousekeeperToday', () => {
  beforeEach(() => setLocale('en'))

  it('counts the dirty rooms and the ones being cleaned, and leads to the dirty list, My tasks and the cleaning list', async () => {
    GET = vi.fn().mockResolvedValue({ data: { data: [{ status: 'DIRTY' }, { status: 'DIRTY' }, { status: 'CLEANING' }, { status: 'CLEAN' }] } })
    const w = await mountCard()
    expect(w.get('[data-testid=hk-today-counts]').text()).toBe('2 dirty, 1 being cleaned.')
    expect(w.get('[data-testid=hk-today-dirty]').attributes('href')).toBe('/housekeeping?status=DIRTY')
    expect(w.get('[data-testid=hk-today-my]').attributes('href')).toBe('/housekeeping/my')
    expect(w.get('[data-testid=hk-today-tasks]').attributes('href')).toBe('/housekeeping/tasks')
  })

  it('still leads the way when the rooms cannot be read', async () => {
    GET = vi.fn().mockRejectedValue(new Error('down'))
    const w = await mountCard()
    expect(w.find('[data-testid=hk-today-failed]').exists()).toBe(true)
    expect(w.find('[data-testid=hk-today-dirty]').exists()).toBe(true)
  })
})
