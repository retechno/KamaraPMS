import { describe, expect, it } from 'vitest'
import { billedPax, isPercent, nextDue, revenueByAccount, rowVariant, tierCovers, tierInputs } from './banquetMoney'

describe('banquetMoney', () => {
  it('sends a blank upper limit as open and a blank percent as 0', () => {
    expect(tierInputs([{ days_from: 0, days_to: null, percent: ' ', action: 'FEE' }, { days_from: 8, days_to: 30, percent: '25', action: 'FEE' }])).toEqual([
      { days_from: 0, days_to: null, percent: '0', action: 'FEE' },
      { days_from: 8, days_to: 30, percent: '25', action: 'FEE' },
    ])
  })

  it('covers the days of a tier with both ends inclusive and an open upper end', () => {
    expect(tierCovers({ days_from: 8, days_to: 30 }, 8)).toBe(true)
    expect(tierCovers({ days_from: 8, days_to: 30 }, 30)).toBe(true)
    expect(tierCovers({ days_from: 8, days_to: 30 }, 31)).toBe(false)
    expect(tierCovers({ days_from: 31, days_to: null }, 400)).toBe(true)
    expect(tierCovers({ days_from: 31, days_to: null }, 30)).toBe(false)
  })

  it('finds the first row not paid and colours the statuses', () => {
    expect(nextDue([{ status: 'PAID' }, { status: 'DUE_SOON' }, { status: 'PENDING' }])?.status).toBe('DUE_SOON')
    expect(nextDue([{ status: 'PAID' }])).toBeUndefined()
    expect(rowVariant('PAID')).toBe('success')
    expect(rowVariant('OVERDUE')).toBe('destructive')
    expect(rowVariant('PENDING')).toBe('outline')
  })

  it('takes a percent from 0 to 100 with up to two decimals', () => {
    expect(['0', '25', '100', '12.5', '12.25'].every(isPercent)).toBe(true)
    expect(['', '101', '-1', '12.345', 'abc'].some(isPercent)).toBe(false)
  })

  it('bills the larger of the guaranteed and the actual guests', () => {
    expect(billedPax(100, 90)).toEqual({ pax: 100, basis: 'GUARANTEED' })
    expect(billedPax(100, 120)).toEqual({ pax: 120, basis: 'ACTUAL' })
    expect(billedPax(null, 80)).toEqual({ pax: 80, basis: 'ACTUAL' })
    expect(billedPax(null, null)).toEqual({ pax: 0, basis: 'NONE' })
  })

  it('groups the revenue by the account of the ledger and falls back to the charge code without one', () => {
    const rows = revenueByAccount([
      { charge_code_id: 1, charge_code: 'BQT_FB', gl_account_code: '4240', gl_account_name: 'F&B', net: '100' },
      { charge_code_id: 2, charge_code: 'BQT_DECOR', gl_account_code: '4240', gl_account_name: 'F&B', net: '50' },
      { charge_code_id: 3, charge_code: 'BQT_OTHER', gl_account_code: null, gl_account_name: null, net: '7' },
    ])
    expect(rows.map((r) => [r.label, r.net])).toEqual([['4240 F&B', 150], ['BQT_OTHER', 7]])
  })
})
