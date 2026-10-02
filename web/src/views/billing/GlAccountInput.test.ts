import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import GlAccountInput from './GlAccountInput.vue'

let GET = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a) } }))

const acc = (id: number, code: string, name: string, type: string, over: Record<string, unknown> = {}) => ({ id, code, name, account_type: type, is_postable: true, is_active: true, ...over })
const accounts = [
  acc(1, '4110', 'Room revenue', 'REVENUE'), acc(2, '2410', 'Tax payable', 'LIABILITY'), acc(3, '1110', 'Cash', 'ASSET'),
  acc(4, '4100', 'Rooms header', 'REVENUE', { is_postable: false }), acc(5, '4199', 'Old revenue', 'REVENUE', { is_active: false }),
]

function mountInput(kind: 'CHARGE_CODE' | 'TAX' | 'SERVICE_CHARGE', value: string, permissions = ['accounting.view']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn().mockResolvedValue({ data: { data: accounts } })
  return mount(GlAccountInput, { props: { kind, modelValue: value, 'onUpdate:modelValue': (v: string) => void w.setProps({ modelValue: v }) }, global: { plugins: [pinia] } })
}
let w: ReturnType<typeof mountInput>

describe('GlAccountInput', () => {
  beforeEach(() => {
    GET = vi.fn()
  })

  it('suggests only the accounts that fit the kind', async () => {
    w = mountInput('CHARGE_CODE', '')
    await flushPromises()
    expect(w.findAll('datalist option').map((o) => o.attributes('value'))).toEqual(['4110'])
    w = mountInput('TAX', '')
    await flushPromises()
    expect(w.findAll('datalist option').map((o) => o.attributes('value'))).toEqual(['2410'])
    w = mountInput('SERVICE_CHARGE', '')
    await flushPromises()
    expect(w.findAll('datalist option').map((o) => o.attributes('value'))).toEqual(['4110', '2410'])
  })

  it('says what the typed code is, or where the amounts go instead', async () => {
    w = mountInput('CHARGE_CODE', '4110')
    await flushPromises()
    expect(w.get('[data-testid=account-hint]').text()).toBe('4110 - Room revenue')
    expect(w.get('[data-testid=account-hint]').classes()).not.toContain('text-destructive')
    for (const [code, text] of [
      ['', 'No account'], ['9999', 'not in the chart'], ['4199', 'inactive'], ['4100', 'header account'], ['1110', 'does not fit'],
    ] as const) {
      await w.get('input').setValue(code)
      const hint = w.get('[data-testid=account-hint]')
      expect(hint.text()).toContain(text)
      expect(hint.text()).toContain('suspense')
      expect(hint.classes()).toContain('text-destructive')
    }
  })

  it('is a plain input without accounting.view', async () => {
    w = mountInput('TAX', '2410', [])
    await flushPromises()
    expect(GET).not.toHaveBeenCalled()
    expect(w.find('datalist').exists()).toBe(false)
    expect(w.find('[data-testid=account-hint]').exists()).toBe(false)
    expect((w.get('input').element as HTMLInputElement).value).toBe('2410')
  })

  it('still works when the chart cannot be loaded', async () => {
    GET = vi.fn().mockRejectedValue(new Error('offline'))
    const pinia = createPinia()
    setActivePinia(pinia)
    useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions: ['accounting.view'] }] } as never
    usePropertyStore().currentId = 7
    const m = mount(GlAccountInput, { props: { kind: 'TAX', modelValue: 'x' }, global: { plugins: [pinia] } })
    await flushPromises()
    expect(m.find('[data-testid=account-hint]').exists()).toBe(false)
    expect(m.find('input').exists()).toBe(true)
  })
})
