# Housekeeping: the bulk change and "My tasks" (ui-uplift 3.2 and 3.4)

## The bulk change of the room status

`POST /properties/{id}/housekeeping/bulk` with `{room_ids (1..100), status, notes?}` changes many rooms to one status. It is the manual change of `POST rooms/{id}/housekeeping` for many rooms, not a new rule.

- **One rule.** `checkTransition(from, to)` is the only place that says what a manual change may do (`DIRTY→CLEANING→CLEAN→INSPECTED`, `DIRTY→CLEAN`, any→`DIRTY`); `SetStatus` and the bulk change both call it. The state of the room (occupied, reserved, blocked) is not part of the rule, here or in the single change. A room that already has the status is a refused transition, not a no-op.
- **Not all-or-nothing.** A room that cannot make the change is reported in `results` with the code the single change gives (`INVALID_HK_TRANSITION` with `from`, `to`, `allowed`; `ROOM_NOT_FOUND` for a room that is not in the property, whoever's it is) and the others change. The request as a whole is refused for what holds for every room: 403 without `housekeeping.update` (and, for `INSPECTED`, without `housekeeping.inspect`; there is one target status per request, so the permission cannot differ per room), 422 for a bad status or a list that is empty or longer than 100, 409 `BUSINESS_DAY_CLOSED`.
- **One transaction, no savepoint.** The open business day is locked once (`FOR SHARE`), then the `room_housekeeping` rows of all the rooms in ascending room id (`ListRoomHousekeepingForUpdate`, lock level 30), then every room is decided from its locked status. A refusal is decided in memory before anything is written, so nothing has to be rolled back for one room. A bulk change and a single change of the same room serialise on the row lock and the later one decides from the status the earlier one left; two bulk changes with the lists in opposite orders cannot deadlock because the rows are always taken in id order. A database failure aborts the whole request (500), as everywhere.
- **Log and audit per room.** Each room that changes goes through `apply()`: one `housekeeping_logs` row (source `MANUAL`, notes "Bulk change" unless the request has some) and one `housekeeping.changed` audit entry, exactly as the single change writes them. A room that is refused writes nothing. There is no summary entry.

## The screen: Room status

With `housekeeping.update`, each tile has a checkbox in its corner; a click on the tile opens the details until a room is selected, then it selects. "Select all shown", "Select floor". A room that a filter hides is dropped from the selection (what the bar acts on is what is on the screen). The cleaning chips show the number of rooms (counted after the other filters). The bar says the number on each button ("Mark clean (12)"); "Inspected" is only there with `housekeeping.inspect`. More than 100 rooms go in requests of 100.

The question (a dialog, not an alert) is asked only when it matters: more than 10 rooms, or a change to dirty that takes rooms that are `INSPECTED` back ("3 rooms that were already inspected will go back to dirty"; `INSPECTED` can only go to `DIRTY`, so no other action lowers them). The result is shown room by room ("10 changed, 2 skipped": "305: already clean"); the skipped rooms stay selected.

## The page "My tasks" (`/housekeeping/my`)

A phone-first page of its own, not a mode of the cleaning list (`/housekeeping/tasks`, the table of the desk): the housekeeper's work is a card per room, the supervisor's is a table. Menu item "My tasks" (group Rooms) for who has `housekeeping.update`.

- **Housekeeper:** `GET housekeeping/tasks?assigned_to=<me>`, in the order of the API (high priority, floor, room). Tabs "Next" and "Finished", floor chips, progress ("6 of 11 rooms finished", skipped counts as handled). A card has the room, type, floor, kind of task, priority, do-not-disturb, make-up request, the note, whether a guest arrives today, and the buttons: **Start** (pending), **Finish** (in progress), **Skip** (both; a sheet asks for the reason, filled in for a do-not-disturb room). After each call, and after a refusal, the list is read again.
- **Supervisor (`housekeeping.assign`):** "Mine / Everyone". Everyone shows the workload of each person (touch one to narrow to their tasks), the assignee on each card, a checkbox on the open tasks and a bar "Assign to…" that opens the list of staff (`housekeeping/staff`) and calls `tasks/assign`; "Nobody (take back)" clears the assignee. The work buttons are on the supervisor's own tasks only.
- **`arrival_today`** on a task (`GET housekeeping/tasks`): a `CONFIRMED` line assigned to the room arrives on the day of the task. No time: the data has no arrival time of a guest.
- **Reading again by itself:** `useAutoRefresh` (every minute and when the tab comes back, not while the tab is hidden, a sheet is open or a read is on its way); the page "Today" uses the same function (`useToday`).
