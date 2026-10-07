import { DOMWrapper, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { refusalOf } from '@/utils/restrictions'
import RestrictionOverride from './RestrictionOverride.vue'

const dlg = (sel: string) => new DOMWrapper(document.body.querySelector(sel) as Element)
const violations = [
  { type: 'STOP_SELL', date: '2026-12-24', room_type_id: 1, rate_plan_id: null, scope: 'ROOM_TYPE', row_id: 3 },
  { type: 'MIN_STAY', date: '2026-12-24', room_type_id: 1, rate_plan_id: null, scope: 'ROOM_TYPE', value: 3, nights: 2, row_id: 4 },
] as never

function mountIt(permissions: string[], overridable = true, error: ApiError | null = null) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'clerk@hotel.com', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  return mount(RestrictionOverride, { props: { violations, overridable, error }, global: { plugins: [pinia] }, attachTo: document.body })
}

describe('RestrictionOverride', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
  })

  it('lists the rules the stay breaks', () => {
    const w = mountIt(['reservation.override_restriction', 'reservation.restriction_approve'])
    expect(w.get('[data-testid=restriction-refusal]').text()).toContain('The night of 2026-12-24 is closed for sale.')
    expect(w.get('[data-testid=restriction-refusal]').text()).toContain('needs at least 3 nights (this one has 2)')
  })

  it('sends the reason alone from a person who approves it themselves', async () => {
    const w = mountIt(['reservation.override_restriction', 'reservation.restriction_approve'])
    expect(w.get('[data-testid=override-restriction]').attributes('disabled')).toBeDefined() // a reason first
    await w.get('input[name=restriction_reason]').setValue('  the owner asked  ')
    await w.get('[data-testid=restriction-form]').trigger('submit')
    expect(w.emitted('override')).toEqual([[{ reason: 'the owner asked' }]])
  })

  it('asks for an approver when the person cannot approve it, and never keeps the password', async () => {
    const w = mountIt(['reservation.override_restriction'])
    await w.get('input[name=restriction_reason]').setValue('the owner asked')
    await w.get('[data-testid=restriction-form]').trigger('submit')
    expect(w.emitted('override')).toBeUndefined()
    await dlg('input[name=approval_password]').setValue('secret')
    await dlg('[data-testid=approval-dialog]').trigger('submit')
    expect(w.emitted('override')).toEqual([[{ reason: 'the owner asked', approval: { email: 'clerk@hotel.com', password: 'secret' } }]])
  })

  it('offers nothing to a person who may not override, and nothing for a booking from the web', () => {
    const none = mountIt(['reservation.update'])
    expect(none.find('[data-testid=restriction-form]').exists()).toBe(false)
    expect(none.get('[data-testid=restriction-not-allowed]').text()).toContain('you may not override')
    const web = mountIt(['reservation.override_restriction'], false)
    expect(web.find('[data-testid=restriction-form]').exists()).toBe(false)
    expect(web.get('[data-testid=restriction-final]').text()).toContain('website')
  })

  it('reads the refusal of the server and nothing else', () => {
    const refused = new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'STAY_RESTRICTED', detail: 'x', context: { violations: [{ type: 'STOP_SELL' }], overridable: false } } as never)
    expect(refusalOf(refused)).toEqual({ violations: [{ type: 'STOP_SELL' }], overridable: false })
    expect(refusalOf(new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'OTHER', detail: 'x' }))).toBeNull()
    expect(refusalOf(null)).toBeNull()
  })
})
