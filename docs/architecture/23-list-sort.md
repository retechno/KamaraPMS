# 23. The order of the paged lists

Status: built (ui-uplift 2.4b, branch `ui-uplift-d2`). Migration 00067. Package `internal/platform/listsort`.

The big lists (reservations, folios, payments, guests, the audit trail) are read a page at a time (50 rows, `limit` and `cursor`). A column of such a list cannot be sorted in the browser:
the browser holds one page, so a sort of it would look like a sort of everything. The order is asked of the server.

## 1. The request

`GET <list>?sort=<column>&order=asc|desc`. Both are optional; `order` without `sort` is refused (422 `VALIDATION_FAILED`, field `order`), as is a column the list does not allow (422, field
`sort`, the allowed columns in the message) or a direction that is not `asc` or `desc`. `order` is `asc` when only `sort` is given. Without `sort` the list has its own default order.

| List | `sort` columns | Default order |
|---|---|---|
| `GET /properties/{id}/reservations` | `arrival_date`, `confirmation_number`, `guest_name`, `status` (the one staff see), `rate` | **nearest first** (below) |
| `GET /properties/{id}/folios` | `folio_number`, `balance`, `status` | open folios first, by room among them (a folio without a room after the rooms; the newest folio of a room first), then the closed ones |
| `GET /properties/{id}/payments` | `payment_number`, `amount`, `payment_method`, `status`, `paid_at` | newest first (`paid_at` desc) |
| `GET /guests` | `name`, `code`, `nationality` | `name` A-Z |
| `GET /properties/{id}/audit-logs`, `GET /audit-logs` | `created_at` | newest first |

The id of the row breaks every tie, in the direction of the order (so a page boundary can fall in the middle of a run of equal values). **NULL sorts last in both directions** (the tables on the
screen do the same): a guest without a nationality, a reservation without a booked price or guest.

**Nearest first** (reservations). The arrivals of the business date and after it come first, soonest first; then the arrivals before it, the most recent first; the confirmation number breaks a
tie. The business date is the open `business_days` row, never the server's date. The cursor of this order carries the date it was made with, so a page goes on from the same date even if the night
audit has run since (a test moves the business day between two pages).

## 2. The cursor

A cursor is an opaque token (`base64url` of JSON): the sort key, the direction, the sort value of the last row of the page (null when that value is NULL), its id and, for the reservations default, the
reference date. It is the keyset of the page: the next page is the rows after `(value, id)` in the order of the list, so a row that is added or removed meanwhile never makes a page repeat or skip.

A cursor belongs to its order. A cursor made for another sort, or for the other direction, is refused: 400 `INVALID_CURSOR` (the same code as a cursor that is not one). It is never read as a position in
the wrong order.

## 3. The queries

sqlc cannot take an `ORDER BY` as a parameter, so a list that can be sorted by several columns has **one query with the key and the direction as parameters** (`ORDER BY` of `CASE` terms, the
keyset predicate of the same shape), plus, where it pays, **a static query for the order the screen opens with**, which the indexes serve:

- guests: `SearchGuests` (name A-Z) and `SearchGuestsDesc` (Z-A, the index read backwards) use `guests_display_name_idx`; `SearchGuestsOrdered` serves `code` and `nationality`;
- payments: `ListPayments` (newest first) uses `payments_paid_at_idx`; `ListPaymentsOrdered` serves the others;
- audit trail: `SearchAuditLogs` and `SearchAuditLogsAsc` (the order is by id, which grows with time: the primary key serves both);
- folios and reservations: one query each (`ListFoliosOrdered`, `SearchReservations`): what they are sorted by is computed.

The reservations query works in two steps: the inner query finds the page on narrow rows (the id and what the order is made of), the outer one reads the wide rows of those ids. Dates, status and rate
of a reservation are derived from its rooms (there are no header columns for them: rejected decision), so the inner query aggregates the rooms of the property in one pass.

## 4. The balance of a folio

The balance is never stored: `SUM(debit) - SUM(credit)` of the folio's items (`03-financial-engines.md`). The list reads it **for the open folios only**; a closed folio is 0 without a look at its
items. That is true because a folio can be closed only with a zero balance (`FOLIO_BALANCE_NOT_ZERO` for `CloseFolio`, `FOLIO_NOT_BALANCED` for the check-out) and nothing is posted to a closed folio
(`FOLIO_CLOSED`). Every database test ends with a check of it (`dbtest.Reset`): no closed folio has a balance. `folio_item_groups` is not read (a transaction group is presentation only).

Measured on 75 000 folios with 375 000 items (300 of the folios open), PostgreSQL 16:

| Query | Time |
|---|---|
| default order (open first, by room), whole list | 80 ms |
| `sort=balance&order=desc`, whole list | 60 ms |
| default, `status=OPEN` (what the screen asks first) | 5 ms |
| (for comparison) the balance of **every** folio summed from the ledger, the index that exists | 620 ms |
| (for comparison) the same with a covering index `(folio_id) INCLUDE (debit, credit)` | 270 ms |

The covering index is **not** built: with the closed folios skipped the existing `folio_items_folio_idx` is enough (open folios only: 4.6 ms; with the covering index 2.2 ms). If a lost invariant ever
made it necessary, the figures above are the reason to build it then.

## 5. Guest names

The list is ordered by the name that is shown: the first name, then the last (a single name is the last name), lower case, in the ICU collation `und-x-icu`, the id breaking a tie. The collation of the
database does not order names: on the Alpine image of PostgreSQL (musl) the `en_US` collation is not applied and the order is by code point: `Eva < Zed < adi < Éric`. With ICU it is `adi < Éric < Eva < Zed`.

`guests_display_name_idx` is the expression of the query, letter for letter, with that collation: `(tenant_id, lower(coalesce(nullif(first_name, '') || ' ', '') || last_name) COLLATE "und-x-icu", id)`.
**The database must have ICU**: migration 00067 stops with a message if `und-x-icu` is missing, and `scripts/db-test.sh` checks the collation and the order it gives. A new version of ICU in the server can
change the order of some characters; then the index must be rebuilt with `REINDEX INDEX guests_display_name_idx` (`docs/deployment.md`).

## 6. JIT

The connections run with `jit = off` (`db.Open`). The planner over-estimates a list that is sorted by a computed value, and PostgreSQL then compiles the query first: 1.4 s for the folio list above, which
runs in 80 ms without it. A hotel's queries are short; JIT only costs here.

## 7. Cost of the other orders (75 000 reservations, 100 000 guests, 100 000 payments)

| Query | Time |
|---|---|
| reservations, nearest first | 210 ms |
| reservations, `rate` / `guest_name` | 320-370 ms |
| guests `name` A-Z or Z-A (index) | under 1 ms |
| guests `code` / `nationality` | 250 ms |
| payments, `amount` | 60-70 ms |
| payments, newest first (index) | under 1 ms |

The reservations are the heavy list: what they are sorted by comes from their rooms, and a hotel's three years of history is 75 000 of them. A year (25 000) costs a third. If it is ever too slow: filter by
arrival (`arrival_from`) by default on the screen, which narrows the rows before the sort.

## 8. The screens

`DataTable` is in `manual` mode for these lists: it shows the rows as the server sent them and tells what was asked (`sortChange`). The order is in the address of the page (`?sort=&order=`), so it can be
shared and the Back button returns to the order before; a change of order reads again from the first page (the cursor is dropped). A column that the server cannot sort is not clickable.
