import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { describe, expect, it } from 'vitest'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import RateOverrideSection, { type RateChange } from './RateOverrideSection.vue'

const nights = [
  { date: '2026-10-02', standard: '1000000', current: '1000000' },
  { date: '2026-10-03', standard: '1000000', current: '900000' },
]

function mountSection(permissions: string[] = ['reservation.override_rate']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  const w = mount(RateOverrideSection, { props: { nights, modelValue: { overrides: [], reason: '' } as RateChange }, global: { plugins: [pinia] } })
  const last = (): RateChange => w.emitted('update:modelValue')!.at(-1)![0] as RateChange
  return { w, last }
}

describe('RateOverrideSection', () => {
  it('stays closed and sends nothing until the rate is changed', () => {
    const { w, last } = mountSection()
    expect(w.find('[data-testid=override-table]').exists()).toBe(false)
    expect(last()).toEqual({ overrides: [], reason: '' })
  })

  it('lists the nights with their standard price and sends only the nights whose price differs from what it is now', async () => {
    const { w, last } = mountSection()
    await w.get('[data-testid=override-toggle]').trigger('click')
    expect(w.get('[data-testid=override-row-2026-10-02]').text()).toContain('1,000,000')
    expect((w.get('input[name=override_amount_2026-10-03]').element as HTMLInputElement).value).toBe('900000') // it starts at what it is now
    expect(last().overrides).toEqual([])
    await w.get('input[name=override_amount_2026-10-02]').setValue('800000')
    expect(last().overrides).toEqual([{ date: '2026-10-02', amount: '800000' }])
    await w.get('input[name=override_amount_2026-10-02]').setValue('1000000') // back to the current price: no change
    expect(last().overrides).toEqual([])
  })

  it('applies one price to every night, and takes the reason', async () => {
    const { w, last } = mountSection()
    await w.get('[data-testid=override-toggle]').trigger('click')
    await w.get('input[name=override_all]').setValue('750000')
    await w.get('[data-testid=override-apply-all]').trigger('click')
    expect(last().overrides).toEqual([{ date: '2026-10-02', amount: '750000' }, { date: '2026-10-03', amount: '750000' }])
    await w.get('input[name=rate_override_reason]').setValue('Corporate rate')
    expect(last().reason).toBe('Corporate rate')
    // closing the editor drops the change
    await w.get('[data-testid=override-toggle]').trigger('click')
    expect(last().overrides).toEqual([])
  })

  it('says whether the person approves their own change or will need an approver', async () => {
    const a = mountSection()
    await a.w.get('[data-testid=override-toggle]').trigger('click')
    expect(a.w.get('[data-testid=override-approval-hint]').text()).toContain('email and password')
    const b = mountSection(['reservation.override_rate', 'reservation.override_rate_approve'])
    await b.w.get('[data-testid=override-toggle]').trigger('click')
    expect(b.w.get('[data-testid=override-approval-hint]').text()).toContain('approve a rate change yourself')
  })
})
