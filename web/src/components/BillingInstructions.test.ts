import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import BillingInstructions from './BillingInstructions.vue'

let GET = vi.fn()
let PUT = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), PUT: (...a: unknown[]) => PUT(...a) } }))

const acme = { id: 4, code: 'ACME', name: 'Acme Corp', is_active: true }
const minibar = { id: 9, code: 'MINIBAR', name: 'Minibar', charge_type: 'FOOD_BEVERAGE', is_active: true }
const room = { id: 1, code: 'ROOM', name: 'Room', charge_type: 'ROOM', is_active: true }
const roomRule = { scope: 'ROOM', charge_code_id: null, company_id: 4, company_name: 'Acme Corp' }

function mountSection(rules: object[], editable = true) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions: ['reservation.read', 'reservation.update'] }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn((path: string) => {
    if (path.endsWith('/billing-instructions')) return Promise.resolve({ data: { instructions: rules } })
    if (path.endsWith('/companies')) return Promise.resolve({ data: { data: [acme] } })
    return Promise.resolve({ data: { data: [room, minibar] } })
  })
  PUT = vi.fn().mockResolvedValue({ data: { instructions: [roomRule] } })
  return mount(BillingInstructions, { props: { reservationId: 9, lineId: 3, editable }, global: { plugins: [pinia] } })
}

describe('BillingInstructions', () => {
  beforeEach(() => {
    GET = vi.fn()
    PUT = vi.fn()
  })

  it('says everything goes to the guest when there is no rule', async () => {
    const w = mountSection([])
    await flushPromises()
    expect(GET.mock.calls[0]).toEqual(['/api/v1/properties/{propertyId}/reservations/{id}/rooms/{lineId}/billing-instructions', { params: { path: { propertyId: 7, id: 9, lineId: 3 } } }])
    expect(w.get('[data-testid=instructions-none-3]').text()).toContain('billed to the guest')
  })

  it('lists who pays what', async () => {
    const w = mountSection([roomRule, { scope: 'CHARGE_CODE', charge_code_id: 9, charge_code: 'MINIBAR', company_id: 4, company_name: 'Acme Corp' }])
    await flushPromises()
    expect(w.get('[data-testid=instruction-3-0]').text()).toBe('The room is billed to Acme Corp')
    expect(w.get('[data-testid=instruction-3-1]').text()).toBe('One charge code MINIBAR is billed to Acme Corp')
  })

  it('has no edit button for a person who cannot update the reservation', async () => {
    const w = mountSection([], false)
    await flushPromises()
    expect(w.find('[data-testid=edit-instructions-3]').exists()).toBe(false)
  })

  it('saves the set: the room to a company, the minibar to the same company', async () => {
    const w = mountSection([])
    await flushPromises()
    await w.get('[data-testid=edit-instructions-3]').trigger('click')
    await flushPromises()
    // the editor starts with a room rule for the first company, and the room code is not offered as a single charge code
    expect(w.findAll('select[name=scope_0]').length).toBe(1)
    await w.get('[data-testid=add-rule]').trigger('click')
    await w.get('select[name=scope_1]').setValue('CHARGE_CODE')
    await w.get('select[name=charge_code_1]').setValue(9)
    expect(w.get('select[name=charge_code_1]').text()).not.toContain('ROOM ·')
    await w.get('[data-testid=instructions-form-3]').trigger('submit')
    await flushPromises()
    expect(PUT.mock.calls[0]).toEqual(['/api/v1/properties/{propertyId}/reservations/{id}/rooms/{lineId}/billing-instructions', {
      params: { path: { propertyId: 7, id: 9, lineId: 3 } },
      body: { instructions: [{ scope: 'ROOM', company_id: 4, charge_code_id: null }, { scope: 'CHARGE_CODE', company_id: 4, charge_code_id: 9 }] },
    }])
    expect(w.find('[data-testid=instructions-form-3]').exists()).toBe(false)
    expect(w.get('[data-testid=instruction-3-0]').text()).toContain('Acme Corp')
  })

  it('shows the refusal and keeps the editor open', async () => {
    const w = mountSection([roomRule])
    await flushPromises()
    PUT.mockRejectedValue(new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'INSTRUCTION_LINE_CLOSED', detail: 'the room is over' }))
    await w.get('[data-testid=edit-instructions-3]').trigger('click')
    await flushPromises()
    await w.get('[data-testid=instructions-form-3]').trigger('submit')
    await flushPromises()
    expect(w.get('[data-testid=instructions-error-3]').text()).toContain('INSTRUCTION_LINE_CLOSED')
    expect(w.find('[data-testid=instructions-form-3]').exists()).toBe(true)
  })
})
