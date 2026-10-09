# 22. Reservation statuses on the screen (UI phase 3)

The reservation screens name the lifecycle of a booking. This note maps what the backend has to the names the screens use, says what is not there, and records how a draft behaves. Nothing here
changes a status, a transition or a rule: the mapping is a label, and every transition is still made and validated by the backend.

## The statuses of the database and the API

| Level | Table / field | Values |
| --- | --- | --- |
| Reservation header | `reservations.status` | `DRAFT`, `CONFIRMED`, `CANCELLED` |
| Room (line) | `reservation_rooms.status` | `DRAFT`, `CONFIRMED`, `CHECKED_IN`, `COMPLETED`, `CANCELLED`, `NO_SHOW` |
| Status staff see | `display_status` (API), `DisplayStatus` in `internal/reservations/model.go` | `DRAFT`, `CONFIRMED`, `IN_HOUSE`, `CHECKED_OUT`, `NO_SHOW`, `CANCELLED` |
| Stay | `stays.status` | `OPEN`, `CHECKED_OUT`, `CANCELLED` (its own lifecycle) |
| Folio | `folios.status` | `OPEN`, `CLOSED` (its own lifecycle) |

`display_status` is derived from the header and its rooms: DRAFT and CANCELLED follow the header; otherwise IN_HOUSE if a room is checked in, else CONFIRMED if a room is still to arrive,
else CHECKED_OUT if a room completed, else NO_SHOW, else CANCELLED. The list computes the same value in SQL (`SearchReservations`); a test (`TestTheListedStatusIsTheOneOfTheDetail`) keeps the two
together.

## The names on the reservation screens

| Screen name | `display_status` | Same meaning? |
| --- | --- | --- |
| Draft | `DRAFT` | yes: created, not confirmed |
| Reserved | `CONFIRMED` | yes: confirmed (the label only; the API value stays CONFIRMED) |
| Checked in | `IN_HOUSE` | yes: at least one room is checked in |
| Checked out | `CHECKED_OUT` | yes: every room that was not cancelled completed |
| Cancelled | `CANCELLED` | yes |
| No-show | `NO_SHOW` | yes: every room that was not cancelled is a no-show |
| **Void** | none | **no such status**: it is not offered |

`web/src/utils/reservationStatus.ts` holds the mapping and the filter; the filter asks the API with `display_status`. Other screens (arrivals, the status badges of lines) keep their labels.

## Void (deferred)

There is no VOID for a reservation: not in the schema, the services, the API or the permissions (the VOID of the city ledger invoices is another thing). It is not the same as CANCELLED, which
has its fee, its folio resolution and its reinstatement. Adding it needs a decision on when it is allowed (no payment and no folio item?), what it does to the inventory and the reports, who may
do it and with which permission, and a migration. Until then the screens show no Void filter and no Void action. Decision of the owner (2026-10): defer.

## A draft and the inventory

A DRAFT holds no inventory. The constraints that count stock and rooms are partial indexes on `status = 'CONFIRMED'` (`reservation_rooms` exclusion and the arrivals index), the availability
engine counts CONFIRMED rooms and the OPEN stays (`internal/availability/queries.sql`), and `TestDraftHoldsNothingAndConfirmClaimsInventory` proves it: creating a draft leaves the availability as it was, and confirming claims it. A
draft can be edited like a confirmed reservation. Confirming runs the availability check, the sales restrictions (an override needs its right), the price of every night and the booker rule (a
confirmed reservation needs a guest). A deposit may be taken on a draft; it is not required by any rule.

## History

The history on the reservation is the audit trail of the reservation (`audit-logs?entity_type=reservation&entity_id=`), the real entries of the services: created, updated, confirmed, cancelled,
room added/amended/assigned/unassigned/cancelled, no-show, reinstated. It needs `audit.read`; a person without it is told so. There is no status history table: a history is not made from the
current status. Gap: the audit entries are listed by their action name; a friendlier text for each action is not written.

## Gaps and what was not done

- No Void (above).
- Confirm, cancel and no-show stay on the reservation page (they ask for a reason or for the restrictions): the list links to the page instead of repeating them.
- Edit Rate for a reservation that is not in house is the rate override of the room on the reservation page (it needs a reason and an approval); for a stay in house it is the editor of the stay.
- The list shows the booked price of the arrival night of each room; a stay that changes price from night to night shows its first night, the detail has all of them.
