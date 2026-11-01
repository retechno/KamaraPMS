import { describe, expect, it } from 'vitest'
import type { NightAuditPreview } from '@/api/types'
import { chargeIssueCount, pendingSteps } from './nightAuditReadiness'

const empty = { unresolved_arrivals: [], unresolved_departures: [], charge_errors: [], invalid_charges: [], accounting_readiness: [], outlet_bills: [], stock: [], open_shifts: [] }
const preview = (over: object = {}, blockers: object = {}) => ({ time_guard_ok: true, can_run: true, blockers: { ...empty, ...blockers }, ...over }) as unknown as NightAuditPreview

describe('pendingSteps, the one meaning of "ready" for the night audit', () => {
  it('is ready (0 steps) only when nothing is in the way', () => {
    expect(pendingSteps(preview())).toEqual({ count: 0, first: null })
    expect(pendingSteps(null)).toEqual({ count: 0, first: null })
  })

  it('counts a step once however many findings it has, in the order of the page', () => {
    expect(pendingSteps(preview({ can_run: false }, { unresolved_arrivals: [{}, {}, {}], unresolved_departures: [{}] }))).toEqual({ count: 2, first: 'arrivals' })
    expect(pendingSteps(preview({ can_run: false }, { unresolved_departures: [{}], open_shifts: [{}] }))).toEqual({ count: 2, first: 'departures' })
  })

  it('counts the clock first', () => {
    expect(pendingSteps(preview({ time_guard_ok: false, can_run: false }, { unresolved_arrivals: [{}] }))).toEqual({ count: 2, first: 'guard' })
  })

  it('holds every kind of finding of the room charges step: errors, invalid charges, accounting, outlets, stock and open shifts', () => {
    for (const key of ['charge_errors', 'invalid_charges', 'accounting_readiness', 'outlet_bills', 'stock', 'open_shifts']) {
      const p = preview({ can_run: false }, { [key]: [{}, {}] })
      expect(chargeIssueCount(p), key).toBe(2)
      expect(pendingSteps(p), key).toEqual({ count: 1, first: 'charges' })
    }
  })

  it('does not say ready when the server refuses the run for a reason the steps do not show', () => {
    expect(pendingSteps(preview({ can_run: false }))).toEqual({ count: 1, first: 'charges' })
  })
})
