package frontdesk_test

import (
	"context"
	"testing"
)

// A stay is named in the audit trail by its own number (STY...), never by the number of the room it is in: the room changes during a stay, the stay does not.
func TestTheAuditEntriesOfAStayAreNamedByTheStayNumber(t *testing.T) {
	f := setup(t)
	res := f.book(t, f.dlx, "2026-09-30", "2026-10-02")
	in, err := f.checkIn(t, f.admin, res, &f.r101, "ci-1")
	must(t, err)
	number := in.Stay.StayNumber
	if number == "" || number == in.StayRoom.RoomNumber {
		t.Fatalf("the stay number %q must not be the room number %q", number, in.StayRoom.RoomNumber)
	}
	label := func(action string) string {
		t.Helper()
		var l *string
		must(t, f.Pool.QueryRow(context.Background(), `SELECT entity_label FROM audit_logs WHERE action = $1 AND entity_type = 'stay' AND entity_id = $2 ORDER BY id DESC LIMIT 1`, action, in.Stay.ID).Scan(&l))
		if l == nil {
			return ""
		}
		return *l
	}
	if got := label("stay.checked_in"); got != number {
		t.Fatalf("check-in: label %q, want the stay number %q (the room is %q)", got, number, in.StayRoom.RoomNumber)
	}
}
