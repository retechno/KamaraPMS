import { readFileSync } from 'node:fs'
import { STATE_FILE } from './env'

/** The desks the tests sign in as. The permissions are codes of internal/platform/auth/permissions.go. */
export const ROLES = {
  frontdesk: {
    name: 'E2E Resepsionis',
    fullName: 'Rina Resepsionis',
    permissions: [
      'reservation.read', 'reservation.create', 'reservation.update', 'reservation.cancel', 'reservation.reinstate', 'reservation.upgrade',
      'guest.read', 'guest.write', 'frontdesk.checkin', 'frontdesk.checkout', 'frontdesk.room_move', 'frontdesk.rate_change',
      'folio.read', 'folio.post_charge', 'payment.post', 'cashier.shift', 'maintenance.report', 'lostfound.report',
    ],
  },
  supervisor: {
    name: 'E2E Supervisor FO',
    fullName: 'Sari Supervisor',
    permissions: [
      'reservation.read', 'reservation.create', 'reservation.update', 'reservation.cancel', 'guest.read', 'guest.write',
      'frontdesk.checkin', 'frontdesk.checkout', 'frontdesk.checkin_unready_room', 'frontdesk.reverse_checkin', 'frontdesk.room_move',
      'folio.read', 'folio.post_charge', 'folio.reverse', 'folio.adjust', 'folio.transfer_any', 'payment.post', 'payment.void', 'payment.refund',
      'correction.approve', 'cashier.shift', 'cashier.shift_manage', 'cashier.shift_approve', 'report.view',
    ],
  },
  cashier: {
    name: 'E2E Kasir',
    fullName: 'Kevin Kasir',
    permissions: ['reservation.read', 'guest.read', 'folio.read', 'folio.post_charge', 'payment.post', 'payment.refund', 'cashier.shift', 'report.view'],
  },
  housekeeping: {
    name: 'E2E Housekeeping',
    fullName: 'Hana Housekeeping',
    permissions: ['housekeeping.update', 'housekeeping.inspect', 'maintenance.report', 'lostfound.report'],
  },
  accounting: {
    name: 'E2E Akuntansi',
    fullName: 'Andi Akuntansi',
    permissions: [
      'accounting.view', 'accounting.post', 'accounting.manage', 'payables.view', 'payables.manage', 'payables.post',
      'bank.view', 'bank.manage', 'bank.reconcile', 'tax.view', 'budget.view', 'budget.manage', 'report.view',
    ],
  },
  pos: {
    name: 'E2E Kasir Outlet',
    fullName: 'Putu Pelayan',
    permissions: ['pos.order', 'pos.cashier', 'pos.charge_room', 'pos.kds', 'pos.report'],
  },
  // The purchasing chain (docs/architecture/35-inventory.md): every level is decided by a different person, never the requester,
  // so a request of the administrator needs one holder per level. invHead is the head of every department (seed.setup.ts).
  invHead: { name: 'E2E Kepala Departemen', fullName: 'Hadi Kepala', permissions: ['inventory.view', 'inventory.dept_approve'] },
  invApprover: { name: 'E2E Penyetuju Gudang', fullName: 'Gita Gudang', permissions: ['inventory.view', 'inventory.approve'] },
  invCost: { name: 'E2E Cost Control', fullName: 'Citra Cost', permissions: ['inventory.view', 'inventory.cost_control'] },
  invFinal: { name: 'E2E Persetujuan Akhir', fullName: 'Fajar Final', permissions: ['inventory.view', 'inventory.final_approve', 'inventory.sr_final_approve'] },
} as const

export type RoleKey = keyof typeof ROLES
export type UserKey = RoleKey | 'admin'

export interface SeedState {
  propertyId: number
  propertyCode: string
  /** The business date when the seed ran; the current one is asked from the API (the night audit moves it). */
  openingBusinessDate: string
  ratePlanId: number
  roomTypes: { id: number; code: string; name: string }[]
  users: Record<UserKey, { email: string; password: string }>
}

let cached: SeedState | null = null
export function seedState(): SeedState {
  if (!cached) cached = JSON.parse(readFileSync(STATE_FILE, 'utf8')) as SeedState
  return cached
}
