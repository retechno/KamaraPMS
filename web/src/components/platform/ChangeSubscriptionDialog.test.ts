import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { setLocale } from '@/i18n'
import { plans, tenant } from '@/views/platform/tenantFixture'
import ChangeSubscriptionDialog from './ChangeSubscriptionDialog.vue'

let PATCH = vi.fn()
vi.mock('@/api/platformClient', () => ({ platformApi: { PATCH: (...a: unknown[]) => PATCH(...a) } }))

const q = <T extends Element>(sel: string) => document.body.querySelector<T>(sel)!
let wrapper: VueWrapper | null = null

async function open(t = tenant(), focus?: 'quotas') {
  wrapper = mount(ChangeSubscriptionDialog, { props: { open: true, tenant: t, plans, focus }, attachTo: document.body })
  await flushPromises()
  return wrapper
}
async function type(sel: string, value: string) {
  const el = q<HTMLInputElement>(sel)
  el.value = value
  el.dispatchEvent(new Event('input', { bubbles: true }))
  await flushPromises()
}
async function choose(sel: string, value: string) {
  const el = q<HTMLSelectElement>(sel)
  el.value = value
  el.dispatchEvent(new Event('change', { bubbles: true }))
  await flushPromises()
}
async function pickDate(name: string, value: string) {
  const el = q<HTMLInputElement>(`input[type=date][name=${name}]`)
  el.value = value
  el.dispatchEvent(new Event('input', { bubbles: true }))
  el.dispatchEvent(new Event('change', { bubbles: true }))
  await flushPromises()
}
const save = async () => {
  q<HTMLButtonElement>('[data-testid=dialog-submit]').click()
  await flushPromises()
}
const saveButton = () => q<HTMLButtonElement>('[data-testid=dialog-submit]')

describe('ChangeSubscriptionDialog', () => {
  beforeEach(async () => {
    PATCH = vi.fn(async () => ({ data: {} }))
    await setLocale('en')
  })
  afterEach(() => {
    wrapper?.unmount()
    wrapper = null
    document.body.innerHTML = ''
  })

  it('starts from the subscription of the tenant, with the plans that are for sale and the current one', async () => {
    await open()
    expect(q('[data-testid=change-subscription-dialog]').textContent).toContain('Change subscription · SGR')
    expect(q<HTMLSelectElement>('select[name=plan_code]').value).toBe('PRO')
    expect([...document.body.querySelectorAll('select[name=plan_code] option')].map((o) => o.getAttribute('value'))).toEqual(['PRO', 'BASIC'])
    expect(q<HTMLSelectElement>('select[name=status]').value).toBe('ACTIVE')
    expect(q<HTMLInputElement>('input[type=date][name=paid_through]').value).toBe('2027-02-08')
    expect(q<HTMLInputElement>('input[name=grace_days]').value).toBe('14')
    expect(q<HTMLInputElement>('input[name=limit_rooms]').value).toBe('')
    expect(q('[data-testid=change-note]').textContent).toContain('issue an invoice with the new plan')
  })

  it('keeps a plan that is no longer for sale in the list when the tenant is on it', async () => {
    await open(tenant({ summary: { plan_code: 'OLD', plan_name: 'Old' } }))
    expect([...document.body.querySelectorAll('select[name=plan_code] option')].map((o) => o.getAttribute('value'))).toEqual(['PRO', 'BASIC', 'OLD'])
  })

  it('shows the quota of the plan as the placeholder, with what is used', async () => {
    await open()
    expect(q<HTMLInputElement>('input[name=limit_rooms]').placeholder).toBe('120')
    expect(q('[data-testid=quota-note-rooms]').textContent).toBe('Empty = follow the plan (120). Used: 100')
    expect(q('[data-testid=quota-note-users]').textContent).toContain('Used: 25')
  })

  it('has the cursor in the plan, or in the first quota when it is opened for the quotas', async () => {
    await open()
    expect(document.activeElement).toBe(q('select[name=plan_code]'))
    wrapper?.unmount()
    document.body.innerHTML = ''
    await open(tenant(), 'quotas')
    expect(document.activeElement).toBe(q('input[name=limit_properties]'))
  })

  it('cannot be saved while nothing has changed', async () => {
    await open()
    expect(saveButton().disabled).toBe(true)
    await type('input[name=grace_days]', '30')
    expect(saveButton().disabled).toBe(false)
  })

  it('sends only what changed: plan, status, date, grace and a quota', async () => {
    const w = await open()
    await choose('select[name=plan_code]', 'BASIC')
    await choose('select[name=status]', 'TRIAL')
    await pickDate('paid_through', '2027-03-01')
    await type('input[name=grace_days]', '30')
    await type('input[name=limit_rooms]', '150')
    await save()
    expect(PATCH).toHaveBeenCalledWith('/api/v1/platform/tenants/{code}/subscription', {
      params: { path: { code: 'SGR' } },
      body: { plan_code: 'BASIC', status: 'TRIAL', paid_through: '2027-03-01', grace_days: 30, limits: { max_rooms: 150 } },
    })
    expect(w.emitted('saved')).toHaveLength(1)
    expect(w.emitted('update:open')?.[0]).toEqual([false])
  })

  it('sends a quota that was set and is emptied in clear_limits', async () => {
    await open(tenant({ subscription: { overrides: { max_properties: null, max_rooms: 150, max_users: null } } }))
    expect(q<HTMLInputElement>('input[name=limit_rooms]').value).toBe('150')
    await type('input[name=limit_rooms]', '')
    await save()
    expect(PATCH.mock.calls[0]![1].body).toEqual({ clear_limits: ['rooms'] })
  })

  it('removes the end of the paid period with "No end date"', async () => {
    await open()
    q<HTMLInputElement>('[data-testid=no-end]').click()
    await flushPromises()
    expect(q<HTMLInputElement>('input[data-slot=date-input-text]').disabled).toBe(true)
    await save()
    expect(PATCH.mock.calls[0]![1].body).toEqual({ no_end: true })
  })

  it('shows "No end date" ticked for a subscription without an end', async () => {
    await open(tenant({ subscription: { paid_through: null }, summary: { paid_through: null } }))
    expect(q<HTMLInputElement>('[data-testid=no-end]').checked).toBe(true)
  })

  it('refuses grace days outside 0 to 90 and a quota below 1, without calling the server', async () => {
    await open()
    await type('input[name=grace_days]', '91')
    await type('input[name=limit_users]', '0')
    await save()
    expect(PATCH).not.toHaveBeenCalled()
    const messages = [...document.body.querySelectorAll('[role=alert]')].map((e) => e.textContent)
    expect(messages).toContain('A whole number from 0 to 90.')
    expect(messages).toContain('A whole number of 1 or more, or empty.')
  })

  it('wants a date for a trial', async () => {
    await open(tenant({ subscription: { paid_through: null }, summary: { paid_through: null } }))
    await choose('select[name=status]', 'TRIAL')
    await save()
    expect(PATCH).not.toHaveBeenCalled()
    expect(document.body.textContent).toContain('A trial ends on a date')
  })

  it('shows a refusal of the server inline and stays open', async () => {
    PATCH = vi.fn(async () => {
      throw new ApiError({ type: 'x', title: 'the plan is retired', status: 409, code: 'PLAN_INACTIVE' })
    })
    const w = await open()
    await choose('select[name=plan_code]', 'BASIC')
    await save()
    expect(q('[data-testid=form-error]').textContent).toContain('the plan is retired')
    expect(w.emitted('saved')).toBeUndefined()
    expect(w.emitted('update:open')).toBeUndefined()
  })

  it('shows the message of a refused field under the field', async () => {
    PATCH = vi.fn(async () => {
      throw new ApiError({ type: 'x', title: 'invalid', status: 422, code: 'VALIDATION_FAILED', errors: [{ field: 'grace_days', code: 'INVALID_VALUE', message: '0 to 90' }] })
    })
    await open()
    await type('input[name=grace_days]', '20')
    await save()
    expect(q('[data-slot=form-field] [role=alert]').textContent).toBe('0 to 90')
  })

  it('closes on Escape, and what was typed is gone when it is opened again', async () => {
    const w = await open()
    await type('input[name=grace_days]', '30')
    q('[data-testid=change-subscription-dialog]').dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    await flushPromises()
    expect(w.emitted('update:open')?.[0]).toEqual([false])
    await w.setProps({ open: false })
    await w.setProps({ open: true })
    await flushPromises()
    expect(q<HTMLInputElement>('input[name=grace_days]').value).toBe('14')
  })
})
