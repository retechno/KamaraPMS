import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { usePropertyStore } from '@/stores/property'
import ChargeCalculator from './ChargeCalculator.vue'

let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { POST: (...a: unknown[]) => POST(...a) } }))

const exclusive = {
  price_mode: 'EXCLUSIVE', quantity: '1', unit_price: '1000000', base_amount: '1000000', discount_amount: '0', net_amount: '1000000',
  rounding_adjustment: '0', service_charge_total: '100000', tax_total: '121000', taxable_amount: '1100000', total_amount: '1221000',
  service_charges: [{ rule_id: 1, code: 'SVC', name: 'Service 10%', rate: '10.0000', base_amount: '1000000', amount: '100000', sequence: 1 }],
  taxes: [{ rule_id: 1, code: 'VAT', name: 'VAT 11%', rate: '11.0000', tax_on_service: true, base_amount: '1100000', amount: '121000', sequence: 1 }],
}

function mountCalc(props: Partial<{ defaultUnitPrice: string; version: number }> = {}) {
  const pinia = createPinia()
  setActivePinia(pinia)
  const property = usePropertyStore()
  property.currentId = 7
  property.current = { currency_code: 'IDR' } as never
  POST = vi.fn().mockResolvedValue({ data: exclusive })
  return mount(ChargeCalculator, { props: { chargeCodeId: 10, priceMode: 'EXCLUSIVE', version: 0, ...props }, global: { plugins: [pinia] } })
}

describe('ChargeCalculator', () => {
  beforeEach(() => {
    POST = vi.fn()
  })

  it('sends the entered values and shows the breakdown', async () => {
    const w = mountCalc()
    await w.get('input[name=unit_price]').setValue('1000000')
    await w.get('[data-testid=calculate]').trigger('click')
    await flushPromises()
    expect(POST).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/charge-calculations', {
      params: { path: { propertyId: 7 } },
      body: { charge_code_id: 10, quantity: '1', unit_price: '1000000' },
    })
    expect(w.get('[data-testid=net]').text()).toBe('1,000,000')
    expect(w.get('[data-testid=service-line]').text()).toContain('10%')
    expect(w.get('[data-testid=tax-line]').text()).toContain('1,100,000')
    expect(w.get('[data-testid=tax-line]').text()).toContain('121,000')
    expect(w.get('[data-testid=total]').text()).toBe('1,221,000')
  })

  it('prefills the default price, and sends discount and price mode only when chosen', async () => {
    const w = mountCalc({ defaultUnitPrice: '250000.00' })
    expect((w.get('input[name=unit_price]').element as HTMLInputElement).value).toBe('250000')
    await w.get('input[name=discount_amount]').setValue('50000')
    await w.get('select[name=price_mode]').setValue('INCLUSIVE')
    await w.get('[data-testid=calculate]').trigger('click')
    await flushPromises()
    expect(POST.mock.calls[0]?.[1]).toMatchObject({ body: { unit_price: '250000', discount_amount: '50000', price_mode: 'INCLUSIVE' } })
  })

  it('reports the rounding adjustment of an inclusive price', async () => {
    const w = mountCalc()
    POST.mockResolvedValue({ data: { ...exclusive, price_mode: 'INCLUSIVE', rounding_adjustment: '0.01', net_amount: '5.74', total_amount: '7.00' } })
    await w.get('[data-testid=calculate]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid=adjustment]').text()).toContain('rounding 0.01')
  })

  it('shows field errors next to the inputs and general errors above', async () => {
    const w = mountCalc()
    POST.mockRejectedValue(
      new ApiError({
        type: 't', title: 'Unprocessable Entity', status: 422, code: 'VALIDATION_FAILED', detail: 'the calculation is invalid',
        errors: [{ field: 'discount_amount', code: 'INVALID_VALUE', message: 'discount 500 must be between 0 and 100' }],
      }),
    )
    await w.get('[data-testid=calculate]').trigger('click')
    await flushPromises()
    expect(w.get('input[name=discount_amount]').attributes('aria-invalid')).toBe('true')
    expect(w.text()).toContain('discount 500 must be between 0 and 100')
    expect(w.find('[data-testid=calc-error]').exists()).toBe(false)

    POST.mockRejectedValue(new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'CHARGE_CODE_INACTIVE', detail: 'the charge code is inactive' }))
    await w.get('[data-testid=calculate]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid=calc-error]').text()).toContain('CHARGE_CODE_INACTIVE')
    expect(w.find('[data-testid=result]').exists()).toBe(false)
  })

  it('recalculates an existing result when the rules were saved', async () => {
    const w = mountCalc()
    await w.get('[data-testid=calculate]').trigger('click')
    await flushPromises()
    expect(POST).toHaveBeenCalledTimes(1)
    await w.setProps({ version: 1 })
    await flushPromises()
    expect(POST).toHaveBeenCalledTimes(2)
  })

  it('does not calculate on its own before the first request', async () => {
    const w = mountCalc()
    await w.setProps({ version: 1 })
    await flushPromises()
    expect(POST).not.toHaveBeenCalled()
  })
})
