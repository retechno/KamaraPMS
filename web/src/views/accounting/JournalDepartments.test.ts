import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { resetDepartments } from '@/composables/useDepartments'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import JournalsView from './JournalsView.vue'

let GET = vi.fn()
let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a) } }))

const journal = { id: 1, journal_number: 'JV000001', journal_type: 'MANUAL', journal_date: '2026-09-30', description: 'Sale', reverses_journal_id: null, reversed_by_journal_id: null, posted_at: '2026-09-30T10:00:00Z', posted_by: 5, approved_by: null, total: '100', line_count: 2 }
const lines = [
  { line_no: 1, account_id: 1, account_code: '1110', account_name: 'Cash on hand', debit: '100', credit: '0' },
  { line_no: 2, account_id: 2, account_code: '4110', account_name: 'Room revenue', debit: '0', credit: '100', department_id: 1, department_code: 'ROOMS', department_name: 'Rooms' },
]
const accounts = [
  { id: 1, code: '1110', name: 'Cash on hand', account_type: 'ASSET', is_postable: true, is_active: true },
  { id: 2, code: '4110', name: 'Room revenue', account_type: 'REVENUE', is_postable: true, is_active: true },
]
const departments = [
  { id: 1, parent_id: null, code: 'ROOMS', name: 'Rooms', sort_order: 10, is_active: true, level: 1, child_count: 0, in_use: true },
  { id: 2, parent_id: null, code: 'FB', name: 'Food and beverage', sort_order: 20, is_active: true, level: 1, child_count: 1, in_use: true },
  { id: 9, parent_id: 2, code: 'REST', name: 'Restaurant', sort_order: 1, is_active: true, level: 2, child_count: 0, in_use: false },
]

function mountView(permissions = ['accounting.view', 'accounting.post']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'a@b.c', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn(async (path: string) => {
    if (path.endsWith('/departments')) return { data: { data: departments } }
    if (path.endsWith('/journals/{id}')) return { data: { ...journal, lines } }
    if (path.endsWith('/journals')) return { data: { data: [journal] } }
    return { data: { data: accounts } }
  })
  POST = vi.fn().mockResolvedValue({ data: { ...journal, id: 4, journal_number: 'JV000004' } })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  return mount(JournalsView, { global: { plugins: [pinia, router] } })
}

describe('the department of a journal line', () => {
  beforeEach(() => resetDepartments())

  it('is chosen per line and posted with the journal', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=new-journal]').trigger('click')
    await flushPromises()
    expect(w.findAll('select[name=department_0] option').map((o) => o.text())).toEqual(['No department', 'ROOMS · Rooms', 'FB · Food and beverage', '– REST · Restaurant'])
    await w.get('input[name=date]').setValue('2026-09-30')
    await w.get('input[name=description]').setValue('Sale')
    await w.get('select[name=account_0]').setValue(1)
    await w.get('select[name=account_1]').setValue(2)
    await w.get('input[name=debit_0]').setValue('100')
    await w.get('input[name=credit_1]').setValue('100')
    await w.get('select[name=department_1]').setValue('9')
    await w.get('[data-testid=journal-form]').trigger('submit')
    await flushPromises()
    const body = POST.mock.calls[0]?.[1].body as { lines: { account_id: number; department_id?: number }[] }
    expect(body.lines[0]?.department_id).toBeUndefined()
    expect(body.lines[1]?.department_id).toBe(9)
  })

  it('is shown on the lines of a journal that is opened', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=journal-JV000001]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid=line-department-2]').text()).toBe('ROOMS')
    expect(w.get('[data-testid=line-department-1]').text()).toBe('')
  })

  it('shows what the server says about a department', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=new-journal]').trigger('click')
    await flushPromises()
    POST.mockRejectedValueOnce(new ApiError({ type: 't', title: 'Invalid', status: 422, code: 'VALIDATION_FAILED', detail: 'the journal is invalid', errors: [{ field: 'lines[1].department_id', code: 'INACTIVE', message: 'the department is switched off' }] }))
    await w.get('input[name=date]').setValue('2026-09-30')
    await w.get('input[name=description]').setValue('x')
    await w.get('select[name=account_0]').setValue(1)
    await w.get('select[name=account_1]').setValue(2)
    await w.get('input[name=debit_0]').setValue('100')
    await w.get('input[name=credit_1]').setValue('100')
    await w.get('[data-testid=journal-form]').trigger('submit')
    await flushPromises()
    expect(w.get('[data-testid=line-1]').text()).toContain('the department is switched off')
  })
})
