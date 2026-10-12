# The room panel (ui-uplift 3.5)

A panel that opens from a room number, from the quick finder (Ctrl+K) and from the Room status board, and tells what is going on in one room today: who is in it, what the folio holds, who comes next, what to do now, and what happened. Design reference: `docs/design/7-panel-kamar.dc.html`.

## The data: one read, `GET {P}/rooms/{roomId}/summary`

A panel that asked for each part itself would need seven or eight calls (the board, the stay, its detail, the folio totals, the reservation, the next arrival, the audit trail, the cleaning log), would half-fail when one of them did, and would repeat the permission rules in the browser. So one endpoint answers, in module `internal/roompanel`.

- **It owns no table and no rule.** Each part is the answer of the service that owns the data, called in the same transaction:

  | Part | Source | Permission that service asks |
  |---|---|---|
  | `room` | `housekeeping.Service.Board` narrowed to the room (`BoardFilter.RoomID`) | a grant on the property |
  | `stay` | `frontdesk.Service.ListStays` (the OPEN stay whose open segment is in the room) and `reservations.Service.Get` (special request, source) | `reservation.read` |
  | `folio` | `folios.Service.StayFolioTotals`: debit, credit and balance of each folio of the stay, from the same `balanceOf` the folio pages use | `folio.read` |
  | `next_arrival` | `frontdesk.Service.NextArrivalOfRoom` (a room with nobody in it): the first CONFIRMED line assigned to the room whose stay has not ended | `reservation.read` |
  | `history` | `housekeeping.Service.Logs` (changes of cleaning on the business date) and `auditlog.Reader.Search` (entries of the room and of its stay on the business date) | the logs: a grant; the audit entries: `audit.read` |

- **Money has one source.** `charges` is the sum of the debits of the folios of the stay, `paid` the sum of their credits, `balance` the sum of their balances (charged minus paid, the number the in-house list shows). There is no second formula; the figures are strings rounded by the folio service at the currency decimals of the property.
- **Parts by permission.** A part the caller may not read is `null`, and its flag in `access` is false; with the flag true `null` means there is nothing there (nobody in the room, no folio, no booking to come). The screen says "nobody is in this room" only when it knows. The audit entry of a cleaning change is not repeated in the history: the change is listed once, as `CLEANING`.
- **One snapshot.** The parts are read in `TxManager.WithinReadTx`: a READ ONLY, REPEATABLE READ transaction, so they agree with each other. Nothing is locked, written or audited.
- **"Today" is the open business date** (`CurrentBusinessDay`), never the date of the server: `business_date`, the night of the stay (`night`, 1 on the arrival date, never above `nights`) and the history all use it. 409 `BUSINESS_DAY_NOT_FOUND` when none is open. A room of another property or tenant is 404 `ROOM_NOT_FOUND` / `PROPERTY_NOT_FOUND`.

## The screen

- `RoomPanel` (a sheet at the right, about 560 px; the whole screen on a phone) is mounted once in the shell (`RoomPanelHost`) and driven by `useRoomPanel()` (`openRoomPanel(roomId, number, seed?)`). The Room status board uses the same component with the row it already has as `seed`, so the sheet is never empty while the summary loads (`room-sheet`, `sheet-block`, `sheet-housekeeping` stay).
- **Quick actions** (the six of the design) are links to the flows that exist, shown only when the role may do them: Receive payment and Add a charge (`/folios/{id}?tab=payment|charge`, the open guest folio), Extend, Move room and Check out (`/stays/{id}?action=extend|move|checkout`, the same pages and wizard the in-house list uses), and Send housekeeping (a small form: kind, priority, note, optional housekeeper; `POST housekeeping/tasks`, `housekeeping.assign`). The next cleaning statuses are buttons for a role that may change the cleaning (`housekeeping.update`, and `housekeeping.inspect` for INSPECTED), plain chips for the others. The keys P, T and O do pay, charge and check-out while the panel has the focus and nothing is being typed.
- **`RoomLink`** is a room number that opens the panel: a `span role="button"` (not a `<button>`, so a table row does not count it among its own buttons), with Enter and Space, that does not click the row it is in. It is in the tables of arrivals, in-house and departures, reservations (list and detail), the tape chart, Today and room charges. Folio pages show no room number, so there is none to link.
- **Ctrl+K** knows room numbers: "305" puts "Room 305 · DLX" first (then rooms whose number starts with it), with the guest and the balance for the room typed in full; Enter opens the panel. "+ 305" is the command "add a charge to room 305": it goes to the charge form of the folio of the guest in that room, for a role that may post charges, and says why when nobody is in the room. The rooms come from the room board, read once a minute at most (`roomDirectory`).

## Tests

`internal/app/roompanel_api_test.go` (an occupied room with a folio, an empty room with a next arrival, each permission, tenant and property isolation, the business date against the server date), `internal/platform/db/readtx_test.go` (snapshot, read only, joining), `internal/roompanel/service_test.go` (night of the stay), and on the web `RoomPanel.test.ts`, `RoomLink.test.ts` and `CommandPalette.rooms.test.ts`.
