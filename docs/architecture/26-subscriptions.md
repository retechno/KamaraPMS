# 26. Subscriptions: plans, modules, quotas and the paid period

Status: **step 1 (backend) built**: migration 00070, `internal/subscription`, the module gate, the quota gate, the read-only state, the `pms-admin` commands and the two read
endpoints, with tests (rules, a quota race, the gates over the real router, invoices). Step 2 (screens) waits for the UI uplift (`docs/ui-uplift-plan.md`) so that it is built once on the new components.

KamaraPMS is sold as SaaS: a **tenant** (a hotel company) pays for a **plan**. The plan decides which optional modules the tenant may use and how much it may hold (properties, active
rooms, active users), and the paid period decides until when it may write. Billing is **manual** for now: the operator issues an invoice and records its payment with `pms-admin`; a
payment gateway can later call the same service.

## 1. Vocabulary

| Term | Meaning |
|---|---|
| Plan | A platform-wide offer (`subscription_plans`): a code, a name, the optional modules it includes, three limits (NULL = unlimited), a price per billing period, a trial length. Not tenant-scoped. |
| Subscription | The one row per tenant (`tenant_subscriptions`): its plan, its status, the paid-through date, the grace days, optional per-tenant limit overrides. |
| Module | A group of API routes that a plan can switch on or off (section 3). `CORE` is always on. |
| Access state | Derived, never stored: `FULL`, `GRACE` or `READ_ONLY` (section 4). |
| Subscription invoice | What the operator bills a tenant for one period (`subscription_invoices`), ISSUED, PAID or VOID. Not a hotel document: it has nothing to do with folios, the city ledger or the property's tax invoices. |

## 2. Tables (migration 00070)

- `subscription_plans`: `code` (unique, upper case), `name`, `modules text[]` (each one of the optional module codes; `CORE` is implied and not listed), `max_properties`,
  `max_rooms`, `max_users` (positive or NULL), `price numeric(18,3) ≥ 0`, `currency_code char(3)`, `billing_period_months` (1, 3, 6 or 12), `trial_days ≥ 0`, `is_active` (a retired plan
  stays on the tenants that have it but cannot be given to another), `is_default` (at most one, partial unique index: the plan of a new tenant).
- `tenant_subscriptions`: `tenant_id` unique (one per tenant, FK to `tenants`), `plan_id`, `status` (`TRIAL`, `ACTIVE`, `CANCELLED`), `started_on`, `paid_through date NULL` (NULL =
  no end: the legacy tenants and internal ones), `grace_days 0..90`, `max_properties_override`, `max_rooms_override`, `max_users_override` (NULL = the plan's), `version`.
- `subscription_invoices`: `number` (unique, `SUB-YYYY-NNNNN`, gapless per year through `subscription_invoice_sequences`), `tenant_id`, `plan_id`, `period_start ≤ period_end`, `amount ≥ 0`,
  `currency_code`, `status` (`ISSUED`, `PAID`, `VOID`), `issued_at`, `due_on`, `paid_at`, `paid_on`, `payment_reference`, `voided_at`, `void_reason`, `notes`. CHECKs tie the status to its
  columns (a PAID invoice has `paid_on` and `paid_at`, a VOID one a reason). A PAID or VOID invoice is final (trigger `subscription_invoice_final`).
- The migration seeds two plans and backfills: **`LEGACY`** (every module, no limits, inactive for new sales) given to every tenant that exists, `ACTIVE`, `paid_through NULL`, so an
  upgrade changes nothing for a running hotel; and **`TRIAL`** (every module, no limits, 30 trial days, `is_default`), the plan a new tenant gets. The operator replaces the default with
  real plans.

Dates are calendar dates in the **tenant's time zone** (`tenants.timezone`), compared with "today" taken from the injected clock. They are not hotel business dates: a subscription runs on
the calendar, whether or not the night audit has run.

## 3. Modules

The module of a request is decided from the **route pattern** the API mux matched (`api.Handler(r)`), not from the URL, because several modules share prefixes such as
`{P}/reservations/{id}/…`. `subscription.ModuleOf(pattern)` is the only classification; `TestEveryRouteHasAModule` reads every route the server registers (the same reading as the
authorization matrix) and fails when one is not classified, so a new route has to choose.

| Module | Routes (P = `/api/v1/properties/{propertyId}`) |
|---|---|
| `CORE` (always) | auth, users, roles, permissions, properties, business days, audit logs, guests, rooms, room types, bed types, room blocks, the housekeeping board, one room's status and its log, rates and yield rules, availability, reservations, front desk, folios, payments, cashier, night audit, room charges, billing configuration, companies, groups, city ledger, documents of these, e-mails, reports and the dashboard except the ones below, `/api/v1/subscription*` |
| `HOUSEKEEPING` | `P/housekeeping/bulk`, `P/housekeeping/tasks*` (the task workflow and "my tasks"), `P/maintenance-*`, `P/lost-found`, `P/reports/housekeeping-*`, `P/reports/maintenance` |
| `ACCOUNTING` | `P/accounting/*`, `P/payables*`, `P/bank*`, `P/tax*` (filing and tax invoices), `P/budgets*`, `P/departments*`, and their PDFs |
| `INVENTORY`, `BANQUET`, `POS` | Reserved: a plan may include them now; their routes join this table when they are built. |

Rules:
- A route of a module that is not in the plan answers **403 `MODULE_NOT_IN_PLAN`** (`context.module`). The check runs after authentication and before the handler.
- Only the HTTP surface is gated. The services keep working underneath: a property without `ACCOUNTING` still gets its day journal at the night audit, a room still turns DIRTY at check-out.
  Turning a module on later therefore shows complete data, and turning it off loses nothing.
- Housekeeping status itself is `CORE`: check-in refuses a dirty room, so a hotel without the module must still be able to mark one clean.

## 4. Access state and the grace period

```
today ≤ paid_through                      → FULL
paid_through < today ≤ paid_through+grace → GRACE      (writes allowed; the API reports days left)
later, or CANCELLED after paid_through    → READ_ONLY  (GET and HEAD only)
paid_through IS NULL                      → FULL
```

- In `READ_ONLY` every other method answers **403 `SUBSCRIPTION_READ_ONLY`**, except `/api/v1/auth/*` (a user can still sign out and change their password). The data stays readable
  and exportable: the hotel is never locked out of its own records by a missed payment.
- A `CANCELLED` subscription gets no grace: it ends at `paid_through`.
- Suspension is the existing `tenants.status = SUSPENDED` (no login at all), unchanged and separate; the operator uses it for abuse, not for late payment.
- `TRIAL` starts with `paid_through = started_on + trial_days - 1`; the first paid invoice turns it `ACTIVE`.

## 5. Quotas

| Resource | Counted | Checked in |
|---|---|---|
| properties | `properties.status = 'ACTIVE'` of the tenant | `tenancy.CreateProperty`, `tenancy.UpdateProperty` (INACTIVE → ACTIVE) |
| rooms | `rooms.is_active` over all properties of the tenant | `rooms.CreateRoom` (active), `rooms.UpdateRoom` (inactive → active) |
| users | `users.is_active` of the tenant (administrators included) | `iam.CreateUser`, `iam.UpdateUser` (inactive → active) |

- Over the limit: **409 `PLAN_LIMIT_REACHED`** with `context.resource`, `limit`, `used`.
- The limit is `override ?? plan limit`; NULL is unlimited.
- **Locking:** the use case first locks the tenant's subscription row `FOR UPDATE` at the new level **`LevelSubscription` (7)**, after the night audit advisory lock and before the business
  day, then counts and inserts. Two concurrent creations of the last free room serialise on that row (race test). The consumer modules only see the small interface
  `entitlement.Gate` (`Hold`, `Check`, `Require`) set by `SetQuotaGate`; nil means unlimited (unit tests, `pms-seed`).
- A plan change that lowers a limit below the usage is allowed: nothing is deactivated, the tenant simply cannot add more until it is under the limit again. The summary shows it.
- Operator tooling (`pms-admin create-admin`) is not limited.

## 6. Invoices and payments (manual)

- `pms-admin issue-invoice -tenant T [-plan P] [-periods N] [-amount X] [-due YYYY-MM-DD]`: the period starts the day after `paid_through` (or today if that is in the past or the
  subscription is a trial ending later than today: then the day after the trial); it covers `N × billing_period_months`; the amount defaults to `N × price`. One ISSUED invoice per tenant may
  overlap another only if one of them is VOID (the service refuses an overlapping open period).
- `pms-admin pay-invoice -number SUB-2026-00001 -paid-on DATE -reference REF`: marks it PAID and, in the same transaction under the subscription lock, moves the subscription:
  `paid_through = max(paid_through, period_end)`, `plan = invoice plan` (an upgrade is billed and then applied), `status = ACTIVE`.
- `pms-admin void-invoice -number … -reason …`: an ISSUED invoice only.
- Every change to a subscription and every invoice event writes the tenant's audit trail (`subscription.*`, `subscription_invoice.*`).

## 7. API (tenant side, read only)

- `GET /api/v1/subscription` (any signed-in user): plan code and name, modules (with `CORE`), status, access state, `paid_through`, `grace_ends_on`, `days_left`, limits, usage. The UI
  uses it to hide modules that are off and to show a banner in GRACE and READ_ONLY.
- `GET /api/v1/subscription/invoices` (tenant administrators): the tenant's invoices, newest first.

Changing plans, limits and periods is an operator action (`pms-admin`), not a tenant action: there is no platform administrator user in the product yet, and this design does not add
one. A platform console with its own sign-in is a later step.

## 8. `pms-admin` commands

```
create-plan  -code C -name N -modules HOUSEKEEPING,ACCOUNTING -price 1500000 -currency IDR -period-months 1 [-max-properties 1 -max-rooms 50 -max-users 10 -trial-days 14 -default]
update-plan  -code C [same flags]            list-plans
subscribe    -tenant T -plan C [-paid-through YYYY-MM-DD|none] [-grace-days 14]   (assigns a plan now, no invoice)
set-limits   -tenant T [-max-properties N|plan] [-max-rooms N|plan] [-max-users N|plan]
cancel-subscription -tenant T                 show-subscription -tenant T
issue-invoice / pay-invoice / void-invoice / list-invoices -tenant T
```

## 9. Step 2 (after the UI uplift)

- The navigation hides the routes of modules that are off (from `GET /api/v1/subscription`).
- A banner in the shell for GRACE ("paid through …, N days of grace left") and READ_ONLY.
- A "Subscription" page under Setup for tenant administrators: plan, usage against limits, invoices.
- i18n keys for `MODULE_NOT_IN_PLAN`, `SUBSCRIPTION_READ_ONLY`, `PLAN_LIMIT_REACHED`.

## 10. Not in this step

A payment gateway, proration, automatic suspension after a long READ_ONLY period, e-mail reminders before `paid_through`, per-property pricing, and a platform console.
