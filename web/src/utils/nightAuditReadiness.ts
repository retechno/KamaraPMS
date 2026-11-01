import type { NightAuditPreview } from '@/api/types'

/** The steps of the night audit page, in the order they are shown. */
export type AuditStep = 'guard' | 'arrivals' | 'departures' | 'charges'

export interface AuditPending {
  /** How many steps still need someone; 0 means the audit can run now. */
  count: number
  /** The first of them, where a link should lead. */
  first: AuditStep | null
}

/** How many findings hold the room charges step: the failed and invalid charges, the accounting, the outlet bills, the stock and the open shifts. */
export function chargeIssueCount(p: NightAuditPreview | null | undefined): number {
  const b = p?.blockers
  if (!b) return 0
  return (b.charge_errors?.length ?? 0) + (b.invalid_charges?.length ?? 0) + (b.accounting_readiness?.length ?? 0) + (b.outlet_bills?.length ?? 0) + (b.stock?.length ?? 0) + (b.open_shifts?.length ?? 0)
}

/**
 * The one meaning of "ready" for the night audit: it is ready when it can run now, which is when no step needs anyone: the clock allows it, no arrival is left unresolved, no departure
 * is left in the house and the room charges have nothing that blocks them. The page, the chip of the top bar and the Today card all read it from the same preview.
 */
export function pendingSteps(p: NightAuditPreview | null | undefined): AuditPending {
  if (!p) return { count: 0, first: null }
  const steps: AuditStep[] = []
  if (p.time_guard_ok === false) steps.push('guard')
  if (p.blockers?.unresolved_arrivals?.length) steps.push('arrivals')
  if (p.blockers?.unresolved_departures?.length) steps.push('departures')
  if (chargeIssueCount(p)) steps.push('charges')
  // The server decides in the end: if it says the run is refused and no step above explains it, one step is still pending.
  if (!steps.length && p.can_run === false) steps.push('charges')
  return { count: steps.length, first: steps[0] ?? null }
}
