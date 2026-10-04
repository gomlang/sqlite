package adapter

import (
	"context"
	"errors"
	"testing"
)

func TestCursorObservesControlBeforeAdvancing(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			db := testDatabase(t, ":memory:", 20)
			control := testControl(t, 0)
			queryControl := testControl(t, 0)
			const query = "SELECT 1 UNION ALL SELECT 2"
			var statement Statement
			var cursor Cursor
			var err error
			if prepared {
				statement, err = Prepare(db, query, control)
				if err != nil {
					t.Fatal(err)
				}
				defer CloseStatement(statement)
				cursor, err = QueryStatement(statement, nil, nil, queryControl)
			} else {
				cursor, err = Query(db, query, nil, nil, queryControl)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer CloseCursor(cursor)
			present, saved, err := Next(cursor)
			if err != nil || !present || ValueInteger(RowValues(saved)[0]) != 1 {
				t.Fatalf("first row: present=%v error=%v", present, err)
			}
			Cancel(queryControl)
			for i := 0; i < 2; i++ {
				if present, _, err := Next(cursor); present || !errors.Is(err, context.Canceled) {
					t.Fatalf("cancelled cursor advanced: present=%v error=%v", present, err)
				}
			}
			if _, cursors, _ := Resources(db); cursors != 0 {
				t.Fatal("cancelled cursor still owns the connection")
			}
			if err := CloseCursor(cursor); err != nil {
				t.Fatal(err)
			}
			if prepared {
				cursor, err = QueryStatement(statement, nil, nil, control)
			} else {
				cursor, err = Query(db, query, nil, nil, control)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer CloseCursor(cursor)
			for _, want := range []int64{1, 2} {
				present, row, err := Next(cursor)
				if err != nil || !present || ValueInteger(RowValues(row)[0]) != want {
					t.Fatalf("reused query: present=%v error=%v", present, err)
				}
			}
			if present, _, err := Next(cursor); present || err != nil {
				t.Fatalf("EOF: present=%v error=%v", present, err)
			}
			Cancel(control)
			if present, _, err := Next(cursor); present || err != nil {
				t.Fatalf("completed cursor lost EOF: present=%v error=%v", present, err)
			}
			closedControl := testControl(t, 0)
			cursor, err = Query(db, query, nil, nil, closedControl)
			if err != nil {
				t.Fatal(err)
			}
			if err := CloseCursor(cursor); err != nil {
				t.Fatal(err)
			}
			Cancel(closedControl)
			if present, _, err := Next(cursor); present || !errors.Is(err, ErrClosed) {
				t.Fatalf("closed cursor lost Closed: present=%v error=%v", present, err)
			}
		})
	}
}
