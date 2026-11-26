package contracts

import "testing"

func TestNotificationCursorBoundToFilters(t *testing.T) {
	sel := NotificationSelection{Unread: true, Service: "leave"}
	cursor := NotificationCursor(100, 80, sel)
	v, e := ParseNotificationSelection("status=unread&service=leave&cursor=" + cursor)
	if e != nil || v.Snapshot != 100 || v.Before != 80 {
		t.Fatal(v, e)
	}
	for _, q := range []string{"cursor=" + cursor, "status=unread&service=documents&cursor=" + cursor, "employee_id=17", "status=all&status=unread", "limit=100000", "cursor=bad"} {
		if _, e := ParseNotificationSelection(q); e == nil {
			t.Fatalf("accepted %s", q)
		}
	}
}
