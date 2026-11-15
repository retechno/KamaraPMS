import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { setLocale } from '@/i18n'
import PlanFormView from './PlanFormView.vue'

let GET = vi.fn()
let POST = vi.fn()
let PATCH = vi.fn()
vi.mock('@/api/platformClient', () => ({ platformApi: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a), PATCH: (...a: unknown[]) => PATCH(...a) } }))

const plan = (o: Record<string, unknown> = {}) => ({
  id: 1,
  code: 'BASIC',
  name: 'Basic',
  modules: ['HOUSEKEEPING'],
  limits: { max_properties: 1, max_rooms: 30, max_users: 5 },
  price: '1500000.000',
  currency_code: 'IDR',
  billing_period_months: 1,
  trial_days: 14,
  is_active: true,
  is_default: true,
  tenant_count: 3,
  ...o,
})
const PRO = plan({ id: 2, code: 'PRO', name: 'Pro', modules: ['HOUSEKEEPING', 'ACCOUNTING'], limits: { max_properties: 3, max_rooms: 120, max_users: null }, price: '7500000', billing_period_months: 3, trial_days: 0, is_default: false, tenant_count: 2 })
const problem = (code: string, status: number, errors?: { field: string; code: string; message?: string }[], detail?: string) => new ApiError({ type: 't', title: code, status, code, detail, errors })

async function mountForm(code?: string) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/platform/plans', component: { template: '<div>list</div>' } },
      { path: '/platform/plans/new', component: PlanFormView },
      { path: '/platform/plans/:code', component: PlanFormView, props: true },
      { path: '/platform/tenants/:code', component: { template: '<div />' } },
    ],
  })
  await router.push(code ? `/platform/plans/${code}` : '/platform/plans/new')
  const wrapper = mount(PlanFormView, { props: code ? { code } : {}, global: { plugins: [router] }, attachTo: document.body })
  await flushPromises()
  return { wrapper, router }
}
const val = (w: ReturnType<typeof mount>, sel: string) => (w.get(sel).element as HTMLInputElement).value
const submit = async (w: ReturnType<typeof mount>) => {
  await w.get('form').trigger('submit')
  await flushPromises()
}

describe('PlanFormView', () => {
  beforeEach(() => {
    GET = vi.fn(async () => ({ data: { items: [plan(), PRO] } }))
    POST = vi.fn(async () => ({ data: { plan: plan({ code: 'NEW' }), affected: [] } }))
    PATCH = vi.fn(async () => ({ data: { plan: PRO, affected: [] } }))
    setLocale('en')
  })

  it('shows a skeleton while the plans load', async () => {
    GET = vi.fn(() => new Promise(() => {}))
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: PlanFormView }, { path: '/platform/plans', component: { template: '<div />' } }] })
    await router.push('/')
    const w = mount(PlanFormView, { global: { plugins: [router] } })
    expect(w.find('[data-testid=plan-loading]').exists()).toBe(true)
    expect(w.get('h1').text()).toBe('New plan')
  })

  it('shows the error when the plans cannot be read', async () => {
    GET = vi.fn(async () => {
      throw problem('INTERNAL', 500, undefined, 'The plans could not be read.')
    })
    const { wrapper } = await mountForm()
    expect(wrapper.get('[role=alert]').text()).toContain('The plans could not be read.')
    expect(wrapper.find('form').exists()).toBe(false)
  })

  it('creates a plan: the code is typed in capitals, the unlimited boxes start ticked, CORE is locked on', async () => {
    const { wrapper, router } = await mountForm()
    expect(wrapper.get('h1').text()).toBe('New plan')
    expect(wrapper.get('input[name=code]').attributes('readonly')).toBeUndefined()
    expect(document.activeElement).toBe(wrapper.get('input[name=code]').element)
    const core = wrapper.get('[data-testid=module-CORE]')
    expect((core.element as HTMLInputElement).checked).toBe(true)
    expect(core.attributes('disabled')).toBeDefined()
    for (const k of ['max_properties', 'max_rooms', 'max_users']) {
      expect((wrapper.get(`[data-testid=none-${k}]`).element as HTMLInputElement).checked).toBe(true)
      expect(wrapper.get(`input[name=${k}]`).attributes('disabled')).toBeDefined()
    }
    expect(wrapper.get('[data-testid=default-hint]').text()).toBe('Currently: BASIC. Only one plan can be the default.')

    await wrapper.get('input[name=code]').setValue('gold-1')
    await wrapper.get('input[name=name]').setValue('Gold')
    await wrapper.get('input[name=price]').setValue('2.000.000')
    await wrapper.get('select[name=billing_period_months]').setValue('12')
    await wrapper.get('[data-testid=module-POS]').setValue(true)
    await wrapper.get('[data-testid=module-BANQUET]').setValue(true)
    await submit(wrapper)

    expect(POST).toHaveBeenCalledWith('/api/v1/platform/plans', {
      body: {
        code: 'GOLD-1',
        name: 'Gold',
        modules: ['POS', 'BANQUET'],
        limits: {},
        price: '2000000',
        currency_code: 'IDR',
        billing_period_months: 12,
        trial_days: 0,
        is_active: true,
        is_default: false,
      },
    })
    expect(router.currentRoute.value.path).toBe('/platform/plans')
  })

  it('edits a plan: the code is read only, the fields are filled, and the plan is sent with PATCH', async () => {
    const { wrapper } = await mountForm('PRO')
    expect(wrapper.get('h1').text()).toBe('Edit plan PRO')
    expect(wrapper.text()).toContain('In use by 2 tenants.')
    expect(wrapper.get('input[name=code]').attributes('readonly')).toBeDefined()
    expect(val(wrapper, 'input[name=code]')).toBe('PRO')
    expect(val(wrapper, 'input[name=name]')).toBe('Pro')
    expect(val(wrapper, 'input[name=price]')).toBe('7,500,000')
    expect(val(wrapper, 'select[name=billing_period_months]')).toBe('3')
    expect(val(wrapper, 'input[name=max_rooms]')).toBe('120')
    expect((wrapper.get('[data-testid=none-max_users]').element as HTMLInputElement).checked).toBe(true)
    expect((wrapper.get('[data-testid=none-trial_days]').element as HTMLInputElement).checked).toBe(true)
    expect((wrapper.get('[data-testid=module-ACCOUNTING]').element as HTMLInputElement).checked).toBe(true)
    expect((wrapper.get('[data-testid=module-POS]').element as HTMLInputElement).checked).toBe(false)
    expect(document.activeElement).toBe(wrapper.get('input[name=name]').element)

    await wrapper.get('input[name=max_rooms]').setValue('150')
    await submit(wrapper)
    expect(PATCH).toHaveBeenCalledTimes(1)
    const [path, init] = PATCH.mock.calls[0]!
    expect(path).toBe('/api/v1/platform/plans/{code}')
    expect(init.params).toEqual({ path: { code: 'PRO' } })
    expect(init.body.code).toBeUndefined()
    expect(init.body.limits).toEqual({ max_properties: 3, max_rooms: 150 })
    expect(init.body.modules).toEqual(['HOUSEKEEPING', 'ACCOUNTING'])
    expect(wrapper.get('[data-testid=plan-saved]').text()).toBe('The plan was saved.')
    expect(wrapper.find('[data-testid=plan-affected]').exists()).toBe(false)
  })

  it('says a plan does not exist', async () => {
    const { wrapper } = await mountForm('NOPE')
    expect(wrapper.get('[data-testid=plan-not-found]').text()).toContain('This plan does not exist.')
    expect(wrapper.find('form').exists()).toBe(false)
  })

  it('the Unlimited and No trial boxes empty the field and disable it; unticking gives a field to fill', async () => {
    const { wrapper } = await mountForm('BASIC')
    expect(val(wrapper, 'input[name=max_properties]')).toBe('1')
    await wrapper.get('[data-testid=none-max_properties]').setValue(true)
    expect(val(wrapper, 'input[name=max_properties]')).toBe('')
    expect(wrapper.get('input[name=max_properties]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid=none-max_properties]').setValue(false)
    expect(wrapper.get('input[name=max_properties]').attributes('disabled')).toBeUndefined()

    // trial: 14 days now; the box empties it and sends 0
    expect(val(wrapper, 'input[name=trial_days]')).toBe('14')
    await wrapper.get('[data-testid=none-trial_days]').setValue(true)
    expect(val(wrapper, 'input[name=trial_days]')).toBe('')
    await wrapper.get('[data-testid=none-max_properties]').setValue(true)
    await submit(wrapper)
    const body = PATCH.mock.calls[0]![1].body
    expect(body.trial_days).toBe(0)
    expect(body.limits).toEqual({ max_rooms: 30, max_users: 5 })
  })

  it('does not send a quota that is neither a number nor unlimited, and says so next to it', async () => {
    const { wrapper } = await mountForm('BASIC')
    await wrapper.get('[data-testid=none-max_users]').setValue(true)
    await wrapper.get('[data-testid=none-max_users]').setValue(false)
    await submit(wrapper)
    expect(PATCH).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('Enter a number, or tick the box.')
    expect(wrapper.get('input[name=max_users]').attributes('aria-invalid')).toBe('true')
  })

  it('names the current default plan, and locks the box on the default plan itself', async () => {
    const pro = await mountForm('PRO')
    expect(pro.wrapper.get('[data-testid=default-hint]').text()).toBe('Currently: BASIC. Only one plan can be the default.')
    expect(pro.wrapper.get('[data-testid=is-default]').attributes('disabled')).toBeUndefined()
    pro.wrapper.unmount()
    const basic = await mountForm('BASIC')
    expect(basic.wrapper.get('[data-testid=default-hint]').text()).toBe('This plan is the default. To change it, make another plan the default.')
    expect(basic.wrapper.get('[data-testid=is-default]').attributes('disabled')).toBeDefined()
  })

  it('says there is no default plan when none is', async () => {
    GET = vi.fn(async () => ({ data: { items: [plan({ is_default: false })] } }))
    const { wrapper } = await mountForm()
    expect(wrapper.get('[data-testid=default-hint]').text()).toContain('There is no default plan yet')
  })

  it('makes a plan the default and sends it', async () => {
    const { wrapper } = await mountForm('PRO')
    await wrapper.get('[data-testid=is-default]').setValue(true)
    await submit(wrapper)
    expect(PATCH.mock.calls[0]![1].body.is_default).toBe(true)
  })

  it('lists the tenants that are over a limit after the save, as a warning', async () => {
    PATCH = vi.fn(async () => ({
      data: {
        plan: PRO,
        affected: [
          { tenant_code: 'SEGARA', resource: 'rooms', used: 140, limit: 120 },
          { tenant_code: 'BALI', resource: 'users', used: 9, limit: 5 },
        ],
      },
    }))
    const { wrapper } = await mountForm('PRO')
    await submit(wrapper)
    const box = wrapper.get('[data-testid=plan-affected]')
    expect(box.classes()).toContain('warning')
    expect(box.findAll('li')).toHaveLength(2)
    expect(box.text()).toContain('SEGARA')
    expect(box.text()).toContain('140 rooms in use, limit 120')
    expect(box.text()).toContain('9 users in use, limit 5')
    expect(box.text()).toContain('never deactivates anything')
    expect(box.get('a').attributes('href')).toBe('/platform/tenants/SEGARA')
  })

  it('shows the errors of the server next to the fields and keeps the form', async () => {
    POST = vi.fn(async () => {
      throw problem('VALIDATION_FAILED', 422, [
        { field: 'code', code: 'INVALID_VALUE', message: '2 to 30 of A-Z, 0-9, - or _' },
        { field: 'name', code: 'REQUIRED', message: '1 to 200 characters' },
      ])
    })
    const { wrapper, router } = await mountForm()
    await wrapper.get('input[name=code]').setValue('x')
    await submit(wrapper)
    expect(wrapper.text()).toContain('2 to 30 of A-Z, 0-9, - or _')
    expect(wrapper.text()).toContain('1 to 200 characters')
    expect(wrapper.get('input[name=code]').attributes('aria-invalid')).toBe('true')
    expect(router.currentRoute.value.path).toBe('/platform/plans/new')
    expect(wrapper.get('button[type=submit]').attributes('disabled')).toBeUndefined()
  })

  it('shows the message of a refusal that names no field (a duplicate code)', async () => {
    POST = vi.fn(async () => {
      throw problem('PLAN_CODE_TAKEN', 409, undefined, 'A plan with this code exists.')
    })
    const { wrapper, router } = await mountForm()
    await wrapper.get('input[name=code]').setValue('BASIC')
    await wrapper.get('input[name=name]').setValue('Again')
    await submit(wrapper)
    expect(router.currentRoute.value.path).toBe('/platform/plans/new')
    expect(wrapper.find('[data-testid=plan-saved]').exists()).toBe(false)
  })

  it('disables the save button while it works', async () => {
    let release: (v: unknown) => void = () => {}
    PATCH = vi.fn(() => new Promise((r) => (release = r)))
    const { wrapper } = await mountForm('PRO')
    await wrapper.get('form').trigger('submit')
    expect(wrapper.get('button[type=submit]').attributes('disabled')).toBeDefined()
    release({ data: { plan: PRO, affected: [] } })
    await flushPromises()
    expect(wrapper.get('button[type=submit]').attributes('disabled')).toBeUndefined()
  })

  it('speaks Indonesian', async () => {
    setLocale('id')
    const { wrapper } = await mountForm('PRO')
    expect(wrapper.get('h1').text()).toBe('Ubah paket PRO')
    expect(wrapper.get('[data-testid=default-hint]').text()).toBe('Saat ini: BASIC. Hanya satu paket yang bisa menjadi bawaan.')
    expect(wrapper.get('[data-testid=none-trial_days]').element.closest('label')?.textContent).toContain('Tanpa trial')
  })
})
