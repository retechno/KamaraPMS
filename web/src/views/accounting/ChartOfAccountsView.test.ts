import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { toTree } from './accountMeta'
import ChartOfAccountsView from './ChartOfAccountsView.vue'

let GET = vi.fn()
let POST = vi.fn()
let PATCH = vi.fn()
let DELETE = vi.fn()
vi.mock('@/api/client', () => ({
  api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a), PATCH: (...a: unknown[]) => PATCH(...a), DELETE: (...a: unknown[]) => DELETE(...a) },
}))

const account = (over: Record<string, unknown>) => ({
  id: 1, code: '4110', name: 'Room revenue - transient', account_type: 'REVENUE', normal_side: 'CREDIT', parent_id: 10, parent_code: '4100', is_postable: true, is_active: true,
  statement_group: 'REV_ROOMS', in_use: false, created_at: '2026-09-30T00:00:00Z', department_requirement: 'OPTIONAL', default_department_id: null, ...over,
})
const accounts = [
  account({ id: 20, code: '4000', name: 'Operating revenue', parent_id: null, parent_code: undefined, is_postable: false, statement_group: undefined, in_use: true }),
  account({ id: 10, code: '4100', name: 'Rooms revenue', parent_id: 20, parent_code: '4000', is_postable: false, in_use: true }),
  account({}),
  account({ id: 2, code: '4120', name: 'Room revenue - group', in_use: true, department_requirement: 'REQUIRED', default_department_id: 3, default_department_code: 'ROOMS' }),
  account({ id: 3, code: '4199', name: 'Old rooms account', is_active: false }),
  account({ id: 30, code: '1210', name: 'Guest ledger', account_type: 'ASSET', normal_side: 'DEBIT', parent_id: null, statement_group: 'RECEIVABLES', in_use: true }),
]

function mountView(permissions = ['accounting.view', 'accounting.manage']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn().mockResolvedValue({ data: { data: accounts } })
  POST = vi.fn().mockResolvedValue({ data: {} })
  PATCH = vi.fn().mockResolvedValue({ data: {} })
  DELETE = vi.fn().mockResolvedValue({})
  return mount(ChartOfAccountsView, { global: { plugins: [pinia] } })
}

/** Runs `fn` with the window of a phone (390 px), then puts the width back. */
async function onAPhone<T>(fn: () => Promise<T>): Promise<T> {
  const wide = window.innerWidth
  Object.defineProperty(window, 'innerWidth', { configurable: true, value: 390 })
  try {
    return await fn()
  } finally {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: wide })
  }
}

describe('toTree', () => {
  it('puts children under their parent, each level by code, with the depth', () => {
    const rows = toTree(accounts as never)
    expect(rows.map((r) => `${r.depth}:${r.account.code}`)).toEqual(['0:1210', '0:4000', '1:4100', '2:4110', '2:4120', '2:4199'])
  })

  it('shows an account whose parent is filtered out at the top', () => {
    const rows = toTree(accounts.filter((a) => a.code !== '4100') as never)
    expect(rows.find((r) => r.account.code === '4110')?.depth).toBe(0)
  })
})

describe('ChartOfAccountsView', () => {
  beforeEach(() => {
    GET = vi.fn()
    POST = vi.fn()
    PATCH = vi.fn()
    DELETE = vi.fn()
    vi.restoreAllMocks()
  })

  it('shows the chart as a tree and hides inactive accounts until asked', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.get('[data-testid=account-4110]').text()).toContain('Room revenue - transient')
    expect(w.get('[data-testid=account-4100]').classes()).toContain('header')
    expect(w.find('[data-testid=account-4199]').exists()).toBe(false)
    await w.get('input[name=inactive]').setValue(true)
    expect(w.find('[data-testid=account-4199]').exists()).toBe(true)
    await w.get('input[name=headers]').setValue(false)
    expect(w.find('[data-testid=account-4100]').exists()).toBe(false)
    await w.get('select[name=filter_type]').setValue('ASSET')
    expect(w.findAll('tbody tr')).toHaveLength(1)
    await w.get('select[name=filter_type]').setValue('')
    await w.get('input[name=q]').setValue('guest')
    expect(w.find('[data-testid=account-1210]').exists()).toBe(true)
    expect(w.find('[data-testid=account-4110]').exists()).toBe(false)
  })

  it('creates an account with a group of its type and a parent that is a header', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=new-account]').trigger('click')
    expect(w.findAll('select[name=parent_id] option').map((o) => o.text())).toEqual(['Top level', '4000 · Operating revenue', '4100 · Rooms revenue'])
    await w.get('input[name=code]').setValue('4170')
    await w.get('input[name=name]').setValue('Late check-out')
    await w.get('select[name=parent_id]').setValue(10)
    await w.get('select[name=statement_group]').setValue('REV_ROOMS')
    await w.get('[data-testid=account-form]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls[0]?.[1].body).toEqual({
      code: '4170', name: 'Late check-out', account_type: 'REVENUE', normal_side: undefined, parent_id: 10, is_postable: true, is_active: true,
      statement_group: 'REV_ROOMS', description: undefined, department_requirement: 'OPTIONAL', default_department_id: null,
    })
    expect(w.find('[data-testid=account-form]').exists()).toBe(false)
  })

  it('sets the department rule of an account, and an account that takes none has no default', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=edit-4110]').trigger('click')
    await w.get('select[name=department_requirement]').setValue('REQUIRED')
    await w.get('[data-testid=account-form]').trigger('submit')
    await flushPromises()
    expect(PATCH.mock.calls[0]?.[1].body).toMatchObject({ department_requirement: 'REQUIRED', default_department_id: 0 })
    await w.get('[data-testid=edit-4110]').trigger('click')
    await w.get('select[name=department_requirement]').setValue('NONE')
    await w.get('[data-testid=account-form]').trigger('submit')
    await flushPromises()
    const body = PATCH.mock.calls[1]?.[1].body as Record<string, unknown>
    expect(body.department_requirement).toBe('NONE')
    expect(body.default_department_id).toBeUndefined()
  })

  it('shows the rule of an account in the list', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.get('[data-testid=rule-4120]').text()).toBe('Required · ROOMS')
    expect(w.find('[data-testid=rule-4110]').exists()).toBe(false) // optional with no default: nothing to show
  })

  it('offers only the groups of the chosen type', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=new-account]').trigger('click')
    await w.get('select[name=account_type]').setValue('ASSET')
    const options = w.findAll('select[name=statement_group] option').map((o) => o.element.getAttribute('value'))
    expect(options).toContain('RECEIVABLES')
    expect(options).not.toContain('REV_ROOMS')
  })

  it('"Add under" starts a new account with the parent, its type and its group', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=add-under-4100]').trigger('click')
    expect((w.get('select[name=account_type]').element as HTMLSelectElement).value).toBe('REVENUE')
    expect((w.get('select[name=parent_id]').element as HTMLSelectElement).value).toBe('10')
    expect((w.get('select[name=statement_group]').element as HTMLSelectElement).value).toBe('REV_ROOMS')
  })

  it('edits without changing the code or type, and shows a refusal', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=edit-4120]').trigger('click')
    expect((w.get('input[name=code]').element as HTMLInputElement).disabled).toBe(true)
    expect((w.get('select[name=account_type]').element as HTMLSelectElement).disabled).toBe(true)
    await w.get('input[name=name]').setValue('Room revenue - groups')
    PATCH.mockRejectedValue(new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'ACCOUNT_IN_USE', detail: 'the system posts to this account' }))
    await w.get('input[name=is_active]').setValue(false)
    await w.get('[data-testid=account-form]').trigger('submit')
    await flushPromises()
    expect(PATCH.mock.calls[0]?.[1].params.path).toEqual({ propertyId: 7, id: 2 })
    expect(PATCH.mock.calls[0]?.[1].body).toMatchObject({ name: 'Room revenue - groups', is_active: false })
    expect(w.get('[data-testid=coa-error]').text()).toContain('ACCOUNT_IN_USE')
  })

  it('deletes only accounts nobody uses, after asking', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.find('[data-testid=delete-4120]').exists()).toBe(false) // in use
    expect(w.find('[data-testid=delete-4100]').exists()).toBe(false) // has children
    vi.spyOn(window, 'confirm').mockReturnValueOnce(false)
    await w.get('[data-testid=delete-4110]').trigger('click')
    expect(DELETE).not.toHaveBeenCalled()
    vi.spyOn(window, 'confirm').mockReturnValueOnce(true)
    await w.get('[data-testid=delete-4110]').trigger('click')
    await flushPromises()
    expect(DELETE.mock.calls[0]?.[1].params.path).toEqual({ propertyId: 7, id: 1 })
    expect(w.get('[data-testid=notice]').text()).toContain('4110 deleted')
  })

  it('checks and imports a CSV file, and lists the rows that are wrong', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=open-import]').trigger('click')
    expect((w.get('[data-testid=import-run]').element as HTMLButtonElement).disabled).toBe(true)
    await w.get('textarea[name=csv]').setValue('code,name,type\n4180,Spa,REVENUE\n')
    POST.mockResolvedValue({ data: { dry_run: true, created: 1, updated: 0 } })
    await w.get('[data-testid=import-check]').trigger('click')
    await flushPromises()
    expect(POST.mock.calls[0]?.[1].body).toEqual({ csv: 'code,name,type\n4180,Spa,REVENUE\n', dry_run: true })
    expect(w.get('[data-testid=import-result]').text()).toContain('would add 1 and update 0')
    POST.mockResolvedValue({ data: { dry_run: false, created: 1, updated: 0 } })
    await w.get('[data-testid=import-form]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls[1]?.[1].body.dry_run).toBe(false)
    expect(w.get('[data-testid=notice]').text()).toContain('1 added')
    // a bad file shows its row errors
    await w.get('[data-testid=open-import]').trigger('click')
    await w.get('textarea[name=csv]').setValue('x')
    POST.mockRejectedValue(new ApiError({ type: 't', title: 'Invalid', status: 422, code: 'VALIDATION_FAILED', detail: 'the file is invalid', errors: [{ field: 'rows[3].parent_code', code: 'NOT_FOUND', message: 'no account has the code NOPE' }] } as never))
    await w.get('[data-testid=import-form]').trigger('submit')
    await flushPromises()
    expect(w.get('[data-testid=coa-error]').text()).toContain('rows[3].parent_code: no account has the code NOPE')
  })

  it('shows only what the role may do', async () => {
    const viewer = mountView(['accounting.view'])
    await flushPromises()
    expect(viewer.find('[data-testid=new-account]').exists()).toBe(false)
    expect(viewer.find('[data-testid=edit-4110]').exists()).toBe(false)
    expect(viewer.find('[data-testid=export]').exists()).toBe(true)
    const none = mountView([])
    await flushPromises()
    expect(none.find('[data-testid=no-access]').exists()).toBe(true)
    expect(GET).not.toHaveBeenCalled()
  })

  it('is a card on a phone: the code and the name, the status in the corner, "Edit" as the main action and the rest in the menu', async () => {
    await onAPhone(async () => {
      const w = mountView()
      await flushPromises()
      expect(w.find('table').exists()).toBe(false)
      const card = w.get('[data-testid=account-4100]') // a header account
      expect(card.text()).toContain('4100')
      expect(card.text()).toContain('Rooms revenue')
      expect(card.find('[data-testid=edit-4100]').exists()).toBe(true)
      await card.get('[data-slot=row-menu-trigger]').trigger('click')
      await flushPromises()
      expect(document.body.querySelector('[data-slot=row-menu] [data-testid=add-under-4100]')).not.toBeNull() // a header takes accounts under it
      expect(document.body.querySelector('[data-slot=row-menu] [data-testid=delete-4100]')).toBeNull() // it is in use
      const free = w.get('[data-testid=account-4110]')
      await free.get('[data-slot=row-menu-trigger]').trigger('click')
      await flushPromises()
      expect(document.body.querySelector('[data-slot=row-menu] [data-testid=delete-4110]')).not.toBeNull()
      w.unmount()
      document.body.innerHTML = ''
    })
  })

  it('has no actions on a card without the permission to manage the chart', async () => {
    await onAPhone(async () => {
      const w = mountView(['accounting.view'])
      await flushPromises()
      expect(w.get('[data-testid=account-4100]').find('[data-slot=row-actions]').exists()).toBe(false)
      w.unmount()
    })
  })
})
