# 33. Who reads the reference data of setup

Status: **approved (2026-10-15)**, built in a PR of its own; no migration, no new permission. Asked by the owner on 2026-10-15 (review of PR 32): the pages of Setup showed to a housekeeper, because the reads behind them asked for a grant on the property and for no permission. Decisions of the owner are marked **D**.

## 1. What was wrong

The reads of the reference data (room types, rooms, bed types, rate plans, rates, restrictions, yield rules, taxes, service charges, charge codes, the rules of approval) called `CanAccess`: any member of the property read them. The menu showed the pages of Setup to everyone except the ones marked for administrators, and the router had no guard by permission, so a typed address opened a page that only answered with what it read.

## 2. The rule (D)

- **The menu and the router.** A page of Setup names **the permission that manages what it shows** (and nothing else: not the permissions of the screens that only use the data). Reception and housekeeping see no section of Setup. The route carries the same permission in `meta.permission`; a person without it who types or keeps the address goes to the home page (as `adminOnly` does).
- **The reads stay any-of.** The endpoint of a read asks for the permission that manages the data **or** one of the permissions of the screens that use it, so that reservation, check-in, walk-in, folio, group booking, city ledger and tax profile keep working. A person with none of them gets `403 PERMISSION_DENIED` (`context.permissions` names the ones that would do). A property the person has no access to is still `404`.
- The writes already asked for the manage permission and are not touched.

| Read | Allowed to | Used by |
|---|---|---|
| `room-types`, `room-types/{id}` | `room.manage`, `reservation.read`, `frontdesk.checkin` | lists of reservations, arrivals, departures, check-in, walk-in, group booking, stay actions |
| `bed-types` | `room.manage`, `reservation.read` | new reservation, reservation detail |
| `rooms`, `rooms/{id}` | `room.manage`, `room_block.manage` | setup of rooms, the picker of room blocks |
| `taxes`, `service-charges` | `billing_config.manage`, `cityledger.credit_note`, `tax.manage` | taxes and charge codes, credit note of the city ledger, tax profiles |
| `charge-codes`, `charge-codes/{id}` | `billing_config.manage`, `folio.read`, `rate.manage` | folio, billing instructions, master folio, rate plans |
| `rate-plans` | `rate.manage`, `reservation.read` | walk-in, lists and detail of reservations, group booking |
| `rate-plans/{id}/bed-adjustments`, `rates`, `rate-restrictions`, `yield-rules` | `rate.manage` | the pages of rates only |
| `rate-restrictions/effective` | `rate.manage`, `reservation.read` | the sales screens |
| `approval-rules` (the table, with who can approve each kind) | `property.manage` | the page of the rules |
| `approval-rules/in-force` (kind, rule, limit) | any grant on the property | the dialog of approval, on every screen that corrects something |

Not changed, because the operational screens need them: `properties/{id}` (the shell reads currency, time zone and the refund methods), `business-date`, `business-days`, `housekeeping`, `housekeeping/tasks`, `room-blocks`, `rooms/{id}/summary`, `rate-quotes`, `charge-calculations`. `free-night-quotas` (`reservation.read`), `companies` (`reservation.read` or `cityledger.read`), the departments (`accounting.view`), the groups (`reservation.read`), the users and roles (tenant administrator) and the audit trail (`audit.read`) already asked for a permission.

## 3. The menu of Setup (D)

| Item | Permission |
|---|---|
| Room types, bed types, rooms | `room.manage` |
| Taxes and service charges, charge codes | `billing_config.manage` |
| Rate plans, free night quotas, rate grid, bed supplements, restrictions, yield rules | `rate.manage` |
| Approval rules, the page of a property | `property.manage` |
| Companies | `company.manage` |
| Audit trail | `audit.read` |
| Properties, users, roles | tenant administrator |

## 4. What changed for the screens that use the data

- **The room blocks** (open to every member): a person without `room_block.manage` sees the list and the calendar of the blocks and cannot create or release one (D). The blocks now carry `room_number`, so the calendar names the rooms that are blocked without the list of rooms; the picker, which needs the list of rooms, is for who may block a room.
- **The dialog of approval** reads `approval-rules/in-force`. The page of the rules reads the full table (with `permission` and `approvers`) and puts what it saved in the store of the dialog.
- The page of the rules is not asked to anyone without `property.manage`: it says so and reads nothing.

## 5. Tests

- Go, by role (`internal/app/setup_read_roles_test.go`): housekeeping gets `403` on every read of setup and `200` on `housekeeping`, `housekeeping/tasks`, `business-date`, `room-blocks` and `approval-rules/in-force`; the reception reads room types, bed types, charge codes, rate plans, the quotas and the rule in force, and gets `403` on rooms, taxes, the grid, the restrictions, the yield rules and the table of the rules; a manager reads what they manage.
- The matrix of authorization (`authz_matrix_test.go`): the list of reads that ask for no permission is shorter, each of its entries with its reason.
- The service tests of rooms, rates, billing configuration and the rules of approval, by permission.
- Web: the menu by role (housekeeping and reception have no Setup; each page to the permission that manages it), the guard of the router (a typed address goes home), the permission of the menu equal to the one of the route, and a scan of the source of the pages that the menu shows to everyone (`views/openPagesReads.test.ts`) that fails when one reads the reference data of setup for everyone.

## 6. Not in this plan

A new permission to read the reference data (the any-of lists use the permissions that exist); a compact endpoint for each list (the screens that use the data need its fields).
