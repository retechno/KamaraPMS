import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import ShiftRequiredNotice from './ShiftRequiredNotice.vue'

let GET = vi.fn()
let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a) } }))

const problem = (code: string, context: Record<string, unknown> = {}) => new ApiError({ type: 't', title: 'x', status: 409, code, detail: 'x', context })

interface World {
  required?: boolean
  shift?: object | null
  permissions?: string[]
}

async function mountNotice(method: string, world: World = {}) {
  const pinia = createPinia()
  setActivePinia(pinia)
  usePropertyStore().currentId = 7
  useAuthStore().me = { properties: [{ id: 7, permissions: world.permissions ?? ['cashier.shift', 'payment.post'] }], user: { is_tenant_admin: false } } as never
  GET = vi.fn(async (path: string, init?: { params: { query?: { drawer?: string } } }) => {
    if (path.endsWith('/cashier/settings')) return { data: { require_shift_for_cash: world.required ?? true } }
    if (path.endsWith('/shifts/current')) return { data: { shift: world.shift ?? null } }
    if (path.endsWith('/suggested-float')) return { data: { opening_float: init?.params.query?.drawer === 'MAIN-2' ? '75000' : '200000' } }
    throw new Error('unexpected GET ' + path)
  })
  const w = mount(ShiftRequiredNotice, { props: { method }, global: { plugins: [pinia] } })
  await flushPromises()
  return w
}

describe('ShiftRequiredNotice', () => {
  beforeEach(() => {
    setLocale('en')
    POST = vi.fn(async () => ({ data: { id: 1 } }))
  })

  it('warns before posting when the cash has no shift to go on, and offers to open one in place', async () => {
    const w = await mountNotice('CASH')
    expect(w.get('[data-testid=shift-required]').text()).toContain('Cash needs an open shift.')
    expect((w.get('input[name=shift_float]').element as HTMLInputElement).value).toBe('200000')
    expect(w.find('[data-testid=shift-open]').exists()).toBe(true)
  })

  it('says nothing for another method, when no shift is asked for, or when the caller has one', async () => {
    expect((await mountNotice('CARD')).find('[data-testid=shift-required]').exists()).toBe(false)
    expect((await mountNotice('CASH', { required: false })).find('[data-testid=shift-required]').exists()).toBe(false)
    expect((await mountNotice('CASH', { shift: { id: 3 } })).find('[data-testid=shift-required]').exists()).toBe(false)
  })

  it('opens the shift with the drawer and the float typed, then goes away and tells the form', async () => {
    const w = await mountNotice('CASH')
    await w.get('input[name=shift_drawer]').setValue('front')
    await w.get('input[name=shift_float]').setValue('150000')
    await w.get('[data-testid=shift-open]').trigger('click')
    await flushPromises()
    expect(POST).toHaveBeenCalledTimes(1)
    expect(POST.mock.calls[0]?.[1]).toMatchObject({ body: { drawer: 'front', opening_float: '150000' } })
    expect(w.emitted('opened')).toHaveLength(1)
    expect(w.find('[data-testid=shift-required]').exists()).toBe(false)
  })

  it('names the holder of a busy drawer, fills in a free drawer and the float it left', async () => {
    POST = vi.fn(async () => {
      throw problem('DRAWER_IN_USE', { drawer: 'MAIN', holder_name: 'Admin Demo', holder_shift_number: 'SH000007', suggested_drawer: 'MAIN-2' })
    })
    const w = await mountNotice('CASH')
    await w.get('[data-testid=shift-open]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid=shift-drawer-busy]').text()).toBe('Drawer MAIN is held by Admin Demo (shift SH000007). Ask them to close it, or open MAIN-2 instead.')
    expect((w.get('input[name=shift_drawer]').element as HTMLInputElement).value).toBe('MAIN-2')
    expect((w.get('input[name=shift_float]').element as HTMLInputElement).value).toBe('75000')
    expect(w.emitted('opened')).toBeUndefined()
  })

  it('shows another refusal as the server worded it and keeps the form', async () => {
    POST = vi.fn(async () => {
      throw problem('SHIFT_ALREADY_OPEN')
    })
    const w = await mountNotice('CASH')
    await w.get('[data-testid=shift-open]').trigger('click')
    await flushPromises()
    expect(w.find('[data-testid=shift-drawer-busy]').exists()).toBe(false)
    expect(w.find('[data-testid=shift-open]').exists()).toBe(true)
    expect(w.text()).toContain('x')
  })

  it('tells a role that cannot open shifts to ask a cashier, without the form', async () => {
    const w = await mountNotice('CASH', { permissions: ['payment.post'] })
    expect(w.get('[data-testid=shift-required]').text()).toContain('Ask a cashier')
    expect(w.find('[data-testid=shift-open]').exists()).toBe(false)
  })

  it('looks again when the method changes to cash', async () => {
    const w = await mountNotice('CARD')
    expect(w.find('[data-testid=shift-required]').exists()).toBe(false)
    await w.setProps({ method: 'CASH' })
    await flushPromises()
    expect(w.find('[data-testid=shift-required]').exists()).toBe(true)
  })
})
