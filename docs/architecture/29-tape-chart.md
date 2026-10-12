# Tape chart: move and change the nights from the chart

The tape chart (`/reservations/tape`) draws **one element per booking** on a CSS grid (a column per night, `grid-column: start / span n`), at 13 px or more, with the guest and the nights. A block of a guest in the house is one bar per room segment of the stay (a stay that moved has several); only the open segment, the one in the room now, can be changed.

Nothing here is a new rule: the chart is another way to reach the endpoints that existed, and **every change asks for confirmation first**. What the chart shows as free or taken is a hint made from the data it already holds; the server decides when the change is confirmed.

## The ways in

- **The menu of a block** (a click, or Enter on the focused block) is the main way, for the keyboard and for a phone: *Move room…* (or *Assign a room…* for a booking with no room), *Change dates…*, *Open the reservation*. An item is not there when the role cannot do it.
- **Drag** (only with a mouse, `pointer: fine`; never on a phone or a touch screen): drag the block to another room to move it, or pull its right end (or the left end of a booking to come) to change the nights. A drop and a pull open the same dialog the menu opens. Esc cancels a drag.
- While a block is dragged, the rooms that cannot take it are marked (hatched, with the reason as a title) and cannot be dropped on: taken on those nights, blocked, another room type without `reservation.upgrade`. A pull that cannot be (a night taken, a stay left with no night after the business date) is refused with the reason while it is made.

## Endpoints, per action

| Action | Endpoint | Right |
|---|---|---|
| Move a booking to come (also: give a room to a booking that has none) | `POST /reservations/{id}/rooms/{lineId}/assign-room` `{version, room_id, upgrade}` | `reservation.update`; `reservation.upgrade` for another type |
| Move a stay in the house | `POST /stays/{id}/move` `{version, room_id, reason, override…}` | `frontdesk.room_move`; the override of a room that is not ready needs `frontdesk.checkin_unready_room` |
| Change the arrival (the left end), booking to come | `PATCH /reservations/{id}/rooms/{lineId}` `{version, arrival_date}` | `reservation.update` |
| Change the departure, booking to come | `PATCH /reservations/{id}/rooms/{lineId}` `{version, departure_date}` | `reservation.update` |
| Extend or shorten a stay in the house | `POST /stays/{id}/change-departure` `{version, departure_date}` | `reservation.update` |

There is no endpoint for the arrival of a stay in the house, so it has no left handle and its arrival field is locked.

The version is **read again when the dialog opens** (`GET /reservations/{id}` or `GET /stays/{id}`), not taken from the chart.

## What the dialogs say

- **Move:** from room to room, the nights (a stay: from the business date to its departure), whether the room is of another type than the one booked ("booked as SUP, in a DLX room": the chart carries the BOOKED type of each line, so an upgraded line is judged right), and that the price of the nights does not change (assign-room and the move of a stay keep the price snapshot). A stay asks for a reason.
- **Dates:** the dates now and new, the nights, and the price:
  - the nights that are **added** are priced by the server (`GET /availability` for the added window, the plan and room type of the line) and shown as "estimate from the server";
  - the nights that are **taken away** are listed with the price they were sold at (the snapshot of the line), with no new total;
  - when the line has a night whose price was changed by hand, belongs to a reservation of a company, or is on a plan that is not paid (complimentary, house use), or the server cannot price the nights, **no figure is shown**: "the system computes the price when it is saved". The client computes no price and no tax.
- **A refusal of the server** (409, 422: the room was taken, the version changed, a night already charged, a sales restriction) is shown in the dialog, and the chart is read again when the dialog is closed. Sales-restriction overrides, price overrides and approvals are not offered here; the reservation page has them.

## The read model

`GET /tape-chart` gives each booking `room_type_id` (the booked type of the line), `stay_id` (null before check-in) and `open_segment`, so the drag can mark the rooms at once, without a fetch.

## Tests

`utils/tape.test.ts` (the verdicts and the columns), `TapeChartView.test.ts` and `TapeChartView.actions.test.ts` (the grid, the menu with and without the rights, a move before and after check-in, an upgrade with and without the permission, an extension with the estimate of the server and without a figure, a drop on a room that is not available, a refusal of the server, the pull of the ends, a phone with no drag), and in Go the fields of the tape chart (`internal/reservations`, `internal/frontdesk`).
