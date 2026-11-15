import type { components } from '@/api/schema'

// Data of the tests of the tenant page and its dialogs.
type Tenant = components['schemas']['PlatformTenantDetail']
type Plan = components['schemas']['PlatformPlan']
type Invoice = components['schemas']['SubscriptionInvoice']

export const plan = (over: Partial<Plan> = {}): Plan => ({
  id: 1,
  code: 'PRO',
  name: 'Pro',
  modules: ['HOUSEKEEPING', 'ACCOUNTING', 'POS'],
  limits: { max_properties: 3, max_rooms: 120, max_users: 30 },
  price: '7500000',
  currency_code: 'IDR',
  billing_period_months: 3,
  trial_days: 14,
  is_active: true,
  is_default: false,
  tenant_count: 4,
  ...over,
})

export const plans: Plan[] = [
  plan(),
  plan({ id: 2, code: 'BASIC', name: 'Basic', price: '2500000', limits: { max_properties: 1, max_rooms: 40, max_users: 10 } }),
  plan({ id: 3, code: 'OLD', name: 'Old', is_active: false }),
]

/** An active tenant on PRO, paid through 8 Feb 2027, nothing overridden. */
export function tenant(over: { status?: Tenant['status']; reason?: string | null; summary?: Partial<Tenant['summary']>; subscription?: Partial<Tenant['subscription']> } = {}): Tenant {
  return {
    code: 'SGR',
    name: 'Segara Resort Group',
    timezone: 'Asia/Makassar',
    created_at: '2026-03-02T01:00:00Z',
    status: over.status ?? 'ACTIVE',
    status_reason: over.reason ?? null,
    status_changed_at: null,
    property_count: 3,
    subscription: {
      id: 1,
      plan_id: 1,
      status: 'ACTIVE',
      started_on: '2026-03-02',
      paid_through: '2027-02-08',
      grace_days: 14,
      overrides: { max_properties: null, max_rooms: null, max_users: null },
      version: 1,
      ...over.subscription,
    },
    summary: {
      plan_code: 'PRO',
      plan_name: 'Pro',
      modules: ['CORE', 'HOUSEKEEPING', 'ACCOUNTING', 'POS'],
      status: 'ACTIVE',
      access: 'FULL',
      started_on: '2026-03-02',
      paid_through: '2027-02-08',
      grace_ends_on: '2027-02-22',
      days_left: 85,
      limits: { max_properties: 3, max_rooms: 120, max_users: 30 },
      usage: { properties: 3, rooms: 100, users: 25 },
      is_trial_ending: false,
      billing_contact: null,
      ...over.summary,
    },
  }
}

export const invoice = (over: Partial<Invoice> = {}): Invoice => ({
  id: 41,
  number: 'SUB-2026-00041',
  plan_id: 1,
  plan_code: 'PRO',
  period_start: '2026-11-09',
  period_end: '2027-02-08',
  amount: '7500000',
  currency_code: 'IDR',
  status: 'ISSUED',
  issued_at: '2026-11-02T03:00:00Z',
  due_on: '2026-11-09',
  is_overdue: false,
  ...over,
})
