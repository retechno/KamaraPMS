import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import PropertyFormView from './PropertyFormView.vue'

let POST = vi.fn()
let GET = vi.fn()
vi.mock('@/api/client', () => ({
  api: {
    POST: (...a: unknown[]) => POST(...a),
    GET: (...a: unknown[]) => GET(...a),
    PATCH: vi.fn(),
  },
}))

async function mountForm() {
  const pinia = createPinia()
  setActivePinia(pinia)
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/setup/properties/new', component: PropertyFormView },
      { path: '/setup/properties', component: { template: '<div>list</div>' } },
    ],
  })
  await router.push('/setup/properties/new')
  const wrapper = mount(PropertyFormView, { global: { plugins: [pinia, router] } })
  return { wrapper, router }
}

describe('PropertyFormView (create)', () => {
  beforeEach(() => {
    POST = vi.fn()
    GET = vi.fn()
  })

  it('defaults the opening date to the property-local today', async () => {
    const { wrapper } = await mountForm()
    const date = (wrapper.get('input[name=opening_business_date]').element as HTMLInputElement).value
    expect(date).toMatch(/^\d{4}-\d{2}-\d{2}$/)
  })

  it('shows server field errors next to the fields', async () => {
    POST.mockRejectedValue(
      new ApiError({
        type: 't', title: 'Unprocessable Entity', status: 422, code: 'VALIDATION_FAILED', detail: 'the property is invalid',
        errors: [
          { field: 'code', code: 'INVALID_FORMAT', message: '1-20 characters: A-Z, 0-9' },
          { field: 'timezone', code: 'INVALID_TIMEZONE', message: 'an IANA time zone such as Asia/Jakarta' },
        ],
      }),
    )
    const { wrapper } = await mountForm()
    await wrapper.get('input[name=code]').setValue('hotel bali')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(wrapper.get('[data-testid=form-error]').text()).toContain('VALIDATION_FAILED')
    expect(wrapper.get('input[name=code]').attributes('aria-invalid')).toBe('true')
    expect(wrapper.text()).toContain('an IANA time zone such as Asia/Jakarta')
  })

  it('submits the property and returns to the list', async () => {
    const created = { id: 7, code: 'BALI', name: 'Hotel Bali', business_date: '2026-09-30' }
    POST.mockResolvedValue({ data: created })
    GET.mockResolvedValue({ data: { ...created, business_date: '2026-09-30' } })
    const { wrapper, router } = await mountForm()
    await wrapper.get('input[name=code]').setValue('BALI')
    await wrapper.get('input[name=name]').setValue('Hotel Bali')
    const methods = wrapper.findAll('input[name=refund_methods]')
    expect(methods.map((m) => (m.element as HTMLInputElement).checked)).toEqual([true, false, false, false]) // cash only
    await methods[2]!.setValue(true) // bank transfer too
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    const [path, opts] = POST.mock.calls[0] as [string, { body: Record<string, unknown> }]
    expect(path).toBe('/api/v1/properties')
    expect(opts.body).toMatchObject({
      code: 'BALI', name: 'Hotel Bali', timezone: 'Asia/Jakarta', currency_code: 'IDR', currency_decimals: 0,
      check_in_time: '14:00', check_out_time: '12:00', night_audit_marks_occupied_dirty: true,
      refund_methods: ['CASH', 'BANK_TRANSFER'],
    })
    expect(opts.body.opening_business_date).toMatch(/^\d{4}-\d{2}-\d{2}$/)
    expect(router.currentRoute.value.path).toBe('/setup/properties')
  })
})

describe('PropertyFormView (edit)', () => {
  const property = (over: object = {}) => ({
    id: 5, code: 'BALI', name: 'Hotel Bali', timezone: 'Asia/Makassar', currency_code: 'IDR', currency_decimals: 0, check_in_time: '14:00', check_out_time: '12:00',
    night_audit_earliest_time: '23:00', require_room_inspection_for_checkin: false, night_audit_marks_occupied_dirty: true, refund_methods: ['CASH'], status: 'ACTIVE',
    business_date: '2026-10-01', currency_locked: false, ...over,
  })

  async function mountEdit(data: object) {
    GET = vi.fn().mockResolvedValue({ data })
    const pinia = createPinia()
    setActivePinia(pinia)
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
    const wrapper = mount(PropertyFormView, { props: { id: '5' }, global: { plugins: [pinia, router] } })
    await flushPromises()
    return wrapper
  }

  it('lets the currency change while the property has no financial data, and says it will lock', async () => {
    const w = await mountEdit(property())
    expect(w.get('input[name=currency_code]').attributes('readonly')).toBeUndefined()
    expect(w.get('select[name=currency_decimals]').attributes('disabled')).toBeUndefined()
    expect(w.find('[data-testid=currency-locked]').exists()).toBe(false)
  })

  it('shows the currency and the decimals read-only, with the reason, once the property has financial data', async () => {
    const w = await mountEdit(property({ currency_locked: true }))
    expect((w.get('input[name=currency_code]').element as HTMLInputElement).value).toBe('IDR')
    expect(w.get('input[name=currency_code]').attributes('readonly')).toBeDefined()
    expect(w.get('select[name=currency_decimals]').attributes('disabled')).toBeDefined()
    expect(w.get('[data-testid=currency-locked]').text()).toContain('nothing is converted')
  })
})
