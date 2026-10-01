import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { useAccountNames } from './accountNames'

let GET = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a) } }))

function host(permissions: string[]) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn().mockResolvedValue({ data: { data: [{ id: 1, code: '4110', name: 'Room revenue' }] } })
  const C = defineComponent({
    setup() {
      const { label } = useAccountNames()
      return { label }
    },
    template: '<p>{{ label("4110") }}|{{ label("9999") }}|{{ label(null) }}</p>',
  })
  return mount(C, { global: { plugins: [pinia] } })
}

describe('useAccountNames', () => {
  it('shows code - name, the bare code when unknown and a dash without a code', async () => {
    const w = host(['accounting.view'])
    await flushPromises()
    expect(w.text()).toBe('4110 - Room revenue|9999|—')
  })

  it('shows the codes as they are without accounting.view', async () => {
    const w = host([])
    await flushPromises()
    expect(GET).not.toHaveBeenCalled()
    expect(w.text()).toBe('4110|9999|—')
  })
})
