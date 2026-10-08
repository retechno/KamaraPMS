# 21. Cancellation fee and no-show fee (manual)

Status: **built** (audit F-08; `internal/folios/fees.go`; no migration). Decided by the owner on 2026-10-08: the fees are posted **by hand**, with an amount the person gives. There is no automatic
calculation, no cancellation-policy engine and no automatic fee at the night audit.

## What it is

A fee is an ordinary charge. `POST /properties/{id}/reservations/{id}/fees` posts the charge code `CANCEL_FEE` or `NO_SHOW_FEE` of the property through the posting path every manual charge uses
(`posting.postCharge`): the charge calculation engine, the charge code snapshot, tax and service charge of that charge code, the department rule, the open business date, the folio item with its
components, the journal at the day close (the revenue account is the one the charge code names; the seeded one is 4510, but nothing in the code names an account), the audit entry and the append-only
ledger. Nothing is inserted by a parallel path. If the property has no tax or service rule on those charge codes, the fee has none.

| | `CANCEL_FEE` | `NO_SHOW_FEE` |
|---|---|---|
| When | the reservation is `CANCELLED` (409 `RESERVATION_NOT_CANCELLED` otherwise) | the room line is `NO_SHOW` (409 `RESERVATION_ROOM_NOT_NO_SHOW` for a confirmed or cancelled room) |
| Subject | the reservation (`reservation_room_id` is refused) | one room: `reservation_room_id` is required and must be a room of this reservation (404 `RESERVATION_ROOM_NOT_FOUND`) |
| Amount | given by the person, above zero, at most the currency's decimals (422 `INVALID_AMOUNT`); the unit price of the charge, so net for an EXCLUSIVE charge code | the same |
| Reason | required, 500 characters at most; kept on the folio item and in the audit entry | the same |

The cancellation policy of a rate plan stays informational text. `Cancel`, `CancelLine`, `NoShow`, `BulkNoShow` and the night audit post nothing: a bulk no-show does not create fees, and the screen
never opens a fee form by itself.

## Charged once

The same event is never charged twice, whatever the amount. The event is identified by facts of the reservation:

- a cancellation: `reservations.cancelled_at` (a reservation that is reinstated and cancelled again has a new one, and that is another event);
- a no-show: the room line and `reservation_rooms.no_show_at`.

The charge carries that identity as its idempotency key (`fee:<TYPE>:<reservation or room id>:<microseconds>`), so the existing unique index `folio_items_idempotency_uk` (property, key) is the
database's guarantee, and no migration was needed. The reservation row is locked first (`FOR UPDATE`, as a deposit does), so the second of two concurrent requests finds the first one's charge and is
refused with **409 `FEE_ALREADY_POSTED`** (`context.folio_item_id`, `folio_id`); the unique index is the last word if anything slips past. A fee that was reversed is still the fee of that event: a
replacement is a new posting by the correction that exists (an adjustment, or a manual charge), never by this endpoint again. No `Idempotency-Key` header is needed or read: a retry after a lost answer
gets the 409 with the id of the fee that was posted.

## The folio

The fee goes to the open folio of the reservation that has **no stay** (the folio deposits go to: `depositFolio`), which is opened when there is none (`folio_created: true`). A cancelled reservation
and a room that did not arrive have no stay, so there is no stay folio to use, and a fee is never put on the folio of another room that is in house. A closed folio is never reused: when the only folio
of the reservation without a stay is closed, a new one is opened (the index allows one open folio without a stay for each reservation). **Billing instructions** (a company pays the room or a
charge code) route the charges of a stay in house to a company folio; that routing exists only for a stay, so a fee of a reservation without one is not routed to a company folio. No new routing rule was
made. A deposit on that folio is netted by the fee as by any charge (a fee of 200,000 against a deposit of 300,000 leaves 100,000 to refund).

## Rules it follows

- The current OPEN business date (`RequireOpenBusinessDay`, and behind it the trigger of migration 00065), never the time of the cancellation or of the no-show. With no open day: `BUSINESS_DAY_NOT_FOUND`.
- Permission `folio.post_charge`. Tenant and property isolation: another tenant's or an unassigned property gives 404 `PROPERTY_NOT_FOUND` / `RESERVATION_NOT_FOUND`.
- Transaction group: the new item is in group A like every new line.
- One transaction: the folio that the fee opens, the item, its components and the audit entry commit together; a posting that fails (for example a required department that nothing supplies) leaves no
  folio behind.
- Audit: `reservation.cancel_fee_posted` / `reservation.no_show_fee_posted` on the reservation, with the type, the amount, the reason, the folio, whether it was opened by the fee, the folio item and the
  charge code (and the room line for a no-show).
- A missing or inactive `CANCEL_FEE` / `NO_SHOW_FEE` charge code gives 409 `FEE_CHARGE_CODE_UNAVAILABLE`.

## Not built (future scope, each a decision of its own)

An automatic amount from the cancellation policy, a structured policy engine, a fee at the night audit or at the bulk no-show, a percentage, first night or full stay calculation, a fee based on the
deposit, a master folio, any payment or refund flow, and any routing of a fee to a company folio for a reservation without a stay.
