import type { ApiError } from '@/api/problem'
import type { Approval, Violation } from '@/api/types'

/** A refused sale and what is needed to go past it: the rules the stay breaks, and whether the booking may override them at all. */
export interface RestrictionRefusal {
  violations: Violation[]
  overridable: boolean
}

/** The override a request carries: a reason, and the approval when the person is not the one who approves. */
export interface RestrictionOverrideInput {
  reason: string
  approval?: Approval
}

/** Reads a 409 STAY_RESTRICTED (or null for any other error): the answer of the server, not a guess of the screen. */
export function refusalOf(error: ApiError | null): RestrictionRefusal | null {
  if (!error || error.code !== 'STAY_RESTRICTED') return null
  const violations = error.context.violations
  return { violations: Array.isArray(violations) ? (violations as Violation[]) : [], overridable: error.context.overridable !== false }
}
