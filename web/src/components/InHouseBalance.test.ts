import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import InHouseBalance from './InHouseBalance.vue'

const balance = (over: object) => ({ amount: '0', status: 'SETTLED', folios: [], ...over }) as never

describe('InHouseBalance', () => {
  it('shows a settled balance as the zero it is', () => {
    const w = mount(InHouseBalance, { props: { balance: balance({ folios: [{}] }) } })
    expect(w.text()).toBe('0')
    expect(w.attributes('data-status')).toBe('SETTLED')
  })

  it('says No folio when there is none, and Unavailable when the balance was not given: neither is a zero', () => {
    const none = mount(InHouseBalance, { props: { balance: balance({ amount: '', status: 'NO_FOLIO' }) } })
    expect(none.text()).toBe('No folio')
    const missing = mount(InHouseBalance, { props: { balance: undefined } })
    expect(missing.text()).toBe('Unavailable')
    expect(missing.attributes('data-status')).toBe('UNAVAILABLE')
    expect(mount(InHouseBalance, { props: { balance: null } }).text()).not.toContain('0')
  })

  it('shows what is owed and a credit with the server amount', () => {
    expect(mount(InHouseBalance, { props: { balance: balance({ amount: '450000', status: 'OUTSTANDING' }) } }).text()).toBe('450,000')
    expect(mount(InHouseBalance, { props: { balance: balance({ amount: '-300000', status: 'CREDIT' }) } }).attributes('data-status')).toBe('CREDIT')
  })
})
