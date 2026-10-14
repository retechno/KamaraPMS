import type { Arrival, GroupRouting } from '@/api/types'
import { guestHints, proposedDeposit, toMinor } from '@/utils/checkin'

/** The rooms of a group check-in go to the server this many at a time, one request after the other, so the screen can show how far it is. The server takes 100 in one request. */
export const GROUP_CHECKIN_CHUNK = 20

export function chunk<T>(items: T[], size: number): T[][] {
  const out: T[][] = []
  for (let i = 0; i < items.length; i += size) out.push(items.slice(i, i + size))
  return out
}

/** Why a row has the deposit it has: nothing is asked (everything goes to the master, or a company pays), the incidental deposit alone (the room goes to the master), or the rule of the check-in panel. */
export type DepositBasis = 'MASTER_ALL' | 'COMPANY' | 'INCIDENTAL' | 'PANEL'

export interface DepositProposal {
  basis: DepositBasis
  /** What is proposed; null when nothing is asked of the row (the column shows a dash). */
  amount: string | null
}

const masterOpen = (routing: GroupRouting | null): boolean => routing?.master?.status === 'OPEN'
const excluded = (a: Arrival, routing: GroupRouting | null): boolean => routing?.lines.find((l) => l.reservation_room_id === a.reservation_room_id)?.excluded ?? false

/** Do the room nights of this row go to the master of the group (an open master, an instruction for the room or for everything, the room not left out)? */
export function roomGoesToMaster(a: Arrival, routing: GroupRouting | null): boolean {
  return masterOpen(routing) && !excluded(a, routing) && !!routing?.instructions.some((i) => i.scope === 'ROOM' || i.scope === 'ALL')
}

/** Does everything the room is charged go to the master? Then no deposit is asked for the room. */
export function everythingGoesToMaster(a: Arrival, routing: GroupRouting | null): boolean {
  return masterOpen(routing) && !excluded(a, routing) && !!routing?.instructions.some((i) => i.scope === 'ALL')
}

/**
 * The deposit proposed for one room of a group (docs/architecture/30-group-folio.md 14):
 * - everything goes to the master: none; a company pays for the room: none;
 * - the room nights go to the master (the other charges stay on the folio of the guest): the incidental deposit of the property alone;
 * - the room is charged on its own folio (left out of the routing, or no instruction for the room): the rule of the check-in panel, the estimate of the stay plus the incidental deposit less what the
 *   reservation already holds. The estimate is the figure of the server (`estimate_total`); a row without one is proposed the incidental deposit alone.
 */
export function depositProposal(a: Arrival, routing: GroupRouting | null, o: { incidental: string; decimals: number }): DepositProposal {
  if (a.company) return { basis: 'COMPANY', amount: null }
  if (everythingGoesToMaster(a, routing)) return { basis: 'MASTER_ALL', amount: null }
  const paid = a.deposit?.paid ?? '0'
  if (roomGoesToMaster(a, routing)) return { basis: 'INCIDENTAL', amount: proposedDeposit({ estimate: null, incidental: o.incidental, paid, decimals: o.decimals }) }
  return { basis: 'PANEL', amount: proposedDeposit({ estimate: a.estimate_total ?? null, incidental: o.incidental, paid, decimals: o.decimals }) }
}

/** Is a deposit to be taken (an amount above zero)? */
export const takesDeposit = (amount: string, decimals: number): boolean => toMinor(amount, decimals) > 0n

/** Can this row be ticked? It is ready, or its only blocker is a room that is not ready and the person may check it in anyway. */
export function rowSelectable(a: Arrival, overrideNotReady: boolean): boolean {
  if (a.status !== 'CONFIRMED') return false
  if (a.readiness.status === 'READY') return true
  return overrideNotReady && a.readiness.blockers.length > 0 && a.readiness.blockers.every((b) => b === 'ROOM_NOT_READY')
}

/** The identity warning of a row (never a block): a foreign guest with no passport number, or a guest with no ID. A placeholder has no profile to judge yet. */
export function identityHint(a: Arrival): 'passport' | 'identity' | null {
  if (a.guest_is_placeholder) return null
  const hints = guestHints({ nationality: a.guest_nationality, id_type: a.guest_id_type ?? (a.guest_has_id ? 'x' : ''), id_number: a.guest_has_id ? 'x' : '', phone: a.guest_has_phone ? 'x' : '' })
  return hints.includes('passport') ? 'passport' : hints.includes('identity') ? 'identity' : null
}
