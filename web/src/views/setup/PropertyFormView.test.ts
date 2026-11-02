import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { setLocale } from '@/i18n'
import PropertyFormView from './PropertyFormView.vue'

let POST = vi.fn()
let GET = vi.fn()
let PATCH = vi.fn()
vi.mock('@/api/client', () => ({
  api: {
    POST: (...a: unknown[]) => POST(...a),
    GET: (...a: unknown[]) => GET(...a),
    PATCH: (...a: unknown[]) => PATCH(...a),
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
    expect(methods.map((m) => (m.element as HTMLInputElement).checked)).toEqual([true, false, false, false, false]) // cash only: QRIS is a choice, never a default
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

  it('can allow refunds by QRIS', async () => {
    POST.mockResolvedValue({ data: { id: 7, code: 'BALI', name: 'Hotel Bali', business_date: '2026-09-30' } })
    GET.mockResolvedValue({ data: { id: 7, code: 'BALI', name: 'Hotel Bali', business_date: '2026-09-30' } })
    const { wrapper } = await mountForm()
    await wrapper.get('input[name=code]').setValue('BALI')
    await wrapper.get('input[name=name]').setValue('Hotel Bali')
    const methods = wrapper.findAll('input[name=refund_methods]')
    expect(methods).toHaveLength(5)
    await methods[4]!.setValue(true)
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect((POST.mock.calls[0] as [string, { body: Record<string, unknown> }])[1].body.refund_methods).toEqual(['CASH', 'QRIS'])
  })
})

describe('PropertyFormView: the language of the standard names', () => {
  beforeEach(() => {
    POST = vi.fn().mockResolvedValue({ data: { id: 7, code: 'BALI', name: 'Hotel Bali', business_date: '2026-09-30' } })
    GET = vi.fn().mockResolvedValue({ data: { id: 7, code: 'BALI', name: 'Hotel Bali', business_date: '2026-09-30' } })
  })
  const submit = async (wrapper: Awaited<ReturnType<typeof mountForm>>['wrapper']) => {
    await wrapper.get('input[name=code]').setValue('BALI')
    await wrapper.get('input[name=name]').setValue('Hotel Bali')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    return (POST.mock.calls[0] as [string, { body: Record<string, unknown> }])[1].body
  }

  it('starts in the language of the page, and sends it with the new property', async () => {
    await setLocale('id')
    const { wrapper } = await mountForm()
    expect((wrapper.get('select[name=data_language]').element as HTMLSelectElement).value).toBe('id')
    expect((await submit(wrapper)).data_language).toBe('id')
    await setLocale('en')
    POST.mockClear()
    const english = await mountForm()
    expect((english.wrapper.get('select[name=data_language]').element as HTMLSelectElement).value).toBe('en')
    expect((await submit(english.wrapper)).data_language).toBe('en')
  })

  it('can be changed to the other language, whatever the language of the page', async () => {
    await setLocale('en')
    const { wrapper } = await mountForm()
    await wrapper.get('select[name=data_language]').setValue('id')
    expect((await submit(wrapper)).data_language).toBe('id')
  })

  it('says what the names are and that they can be changed later, that a charge code is printed on the folio and the invoice, and that properties that exist do not change', async () => {
    await setLocale('id')
    const { wrapper } = await mountForm()
    const card = wrapper.get('[data-testid=data-language-card]').text()
    expect(card).toContain('Nama akun, departemen, dan kode biaya')
    expect(card).toContain('Bisa diubah nanti')
    expect(card).toContain('Nama kode biaya tercetak di folio dan invoice tamu')
    expect(card).toContain('Properti yang sudah ada tidak berubah')
    expect(wrapper.findAll('select[name=data_language] option').map((o) => o.text())).toEqual(['Indonesia', 'English'])
    await setLocale('en')
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

  it('shows the incidental deposit of check-in, and sends it only when it changed', async () => {
    const w = await mountEdit(property({ checkin_incidental_deposit: '250000' }))
    expect((w.get('input[name=checkin_incidental_deposit]').element as HTMLInputElement).value).toBe('250000')
    PATCH = vi.fn().mockResolvedValue({ data: property() })
    await w.get('input[name=name]').setValue('Hotel Bali 2')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect((PATCH.mock.calls[0] as [string, { body: Record<string, unknown> }])[1].body).toEqual({ name: 'Hotel Bali 2' })
    PATCH = vi.fn().mockResolvedValue({ data: property() })
    await w.get('input[name=checkin_incidental_deposit]').setValue('300000')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect((PATCH.mock.calls[0] as [string, { body: Record<string, unknown> }])[1].body).toMatchObject({ checkin_incidental_deposit: '300000' })
  })

  it('a new property proposes no incidental deposit', async () => {
    const created = { id: 7, code: 'BALI', name: 'Hotel Bali', business_date: '2026-09-30' }
    POST = vi.fn().mockResolvedValue({ data: created })
    GET = vi.fn().mockResolvedValue({ data: created })
    const pinia = createPinia()
    setActivePinia(pinia)
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
    const w = mount(PropertyFormView, { global: { plugins: [pinia, router] } })
    await flushPromises()
    expect((w.get('input[name=checkin_incidental_deposit]').element as HTMLInputElement).value).toBe('0')
    await w.get('input[name=code]').setValue('BALI')
    await w.get('input[name=name]').setValue('Hotel Bali')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect((POST.mock.calls[0] as [string, { body: Record<string, unknown> }])[1].body.checkin_incidental_deposit).toBe('0')
  })

  it('chooses the release of deposits until the first transaction, and sends it only when it changed', async () => {
    const w = await mountEdit(property({ deposit_release_mode: 'EVERY_DAY', deposit_release_locked_on: null }))
    expect((w.get('[data-testid=deposit-release-EVERY_DAY]').element as HTMLInputElement).checked).toBe(true)
    expect(w.get('[data-testid=deposit-release-ON_CLOSE]').attributes('disabled')).toBeUndefined()
    expect(w.find('[data-testid=deposit-release-locked]').exists()).toBe(false)
    PATCH = vi.fn().mockResolvedValue({ data: property() })
    await w.get('input[name=name]').setValue('Hotel Bali 2')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect((PATCH.mock.calls[0] as [string, { body: Record<string, unknown> }])[1].body).toEqual({ name: 'Hotel Bali 2' })
    PATCH = vi.fn().mockResolvedValue({ data: property() })
    await w.get('[data-testid=deposit-release-ON_CLOSE]').setValue(true)
    await w.get('form').trigger('submit')
    await flushPromises()
    expect((PATCH.mock.calls[0] as [string, { body: Record<string, unknown> }])[1].body).toMatchObject({ deposit_release_mode: 'ON_CLOSE' })
  })

  it('shows the release of deposits read-only, with the date, once the property has had a transaction', async () => {
    const w = await mountEdit(property({ deposit_release_mode: 'ON_CLOSE', deposit_release_locked_on: '2026-09-30' }))
    expect((w.get('[data-testid=deposit-release-ON_CLOSE]').element as HTMLInputElement).checked).toBe(true)
    expect(w.get('[data-testid=deposit-release-EVERY_DAY]').attributes('disabled')).toBeDefined()
    expect(w.get('[data-testid=deposit-release-ON_CLOSE]').attributes('disabled')).toBeDefined()
    expect(w.get('[data-testid=deposit-release-locked]').text()).toContain('Locked since 30 Sep 2026')
  })

  it('a new property releases every day unless it is told otherwise', async () => {
    const created = { id: 7, code: 'BALI', name: 'Hotel Bali', business_date: '2026-09-30' }
    POST = vi.fn().mockResolvedValue({ data: created })
    GET = vi.fn().mockResolvedValue({ data: created })
    const pinia = createPinia()
    setActivePinia(pinia)
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
    const w = mount(PropertyFormView, { global: { plugins: [pinia, router] } })
    await flushPromises()
    expect((w.get('[data-testid=deposit-release-EVERY_DAY]').element as HTMLInputElement).checked).toBe(true)
    await w.get('input[name=code]').setValue('BALI')
    await w.get('input[name=name]').setValue('Hotel Bali')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect((POST.mock.calls[0] as [string, { body: Record<string, unknown> }])[1].body.deposit_release_mode).toBe('EVERY_DAY')
  })

  it('has no choice of the language of the standard names: it is for a new property only', async () => {
    const w = await mountEdit(property())
    expect(w.find('[data-testid=data-language-card]').exists()).toBe(false)
    expect(w.find('select[name=data_language]').exists()).toBe(false)
  })

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

describe('PropertyFormView, what is seen is what is sent', () => {
  it('does not send while a date is half typed, and says why', async () => {
    const { wrapper: w } = await mountForm()
    await w.get('form input[data-slot=date-input-text]').setValue('3/2')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(POST).not.toHaveBeenCalled()
    expect(w.text()).toContain('Write as dd/mm/yyyy')
  })
})
