package adapter

import (
	"errors"
	"testing"
)

func TestTransactionControlAfterSQLiteWhitespace(t *testing.T) {
	for _, prefix := range []string{"\ufeff", " \ufeff/* comment */\ufeff"} {
		t.Run(prefix, func(t *testing.T) {
			db := testDatabase(t, ":memory:", 25)
			control := testControl(t, 0)
			for _, statement := range []string{"BEGIN", "COMMIT", "END", "ROLLBACK", "SAVEPOINT s", "RELEASE s"} {
				if _, _, err := Execute(db, prefix+statement, nil, nil, control); !errors.Is(err, ErrArgument) {
					t.Fatalf("transaction SQL %q was not rejected: %v", statement, err)
				}
			}
			if _, _, err := Execute(db, prefix+"SELECT 1", nil, nil, control); err != nil {
				t.Fatalf("valid SQL after a BOM was rejected: %v", err)
			}
		})
	}
}

func TestTransactionControlCannotCommitManagedWork(t *testing.T) {
	for _, entry := range []string{"execute", "query", "prepare"} {
		t.Run(entry, func(t *testing.T) {
			db := testDatabase(t, ":memory:", 25)
			control := testControl(t, 0)
			if _, _, err := Execute(db, "CREATE TABLE work(x)", nil, nil, control); err != nil {
				t.Fatal(err)
			}
			tx, err := Begin(db, 0, control)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := Execute(tx, "INSERT INTO work VALUES(7)", nil, nil, control); err != nil {
				t.Fatal(err)
			}
			switch entry {
			case "execute":
				_, _, err = Execute(tx, "\ufeffCOMMIT", nil, nil, control)
			case "query":
				_, err = Query(tx, "\ufeffCOMMIT", nil, nil, control)
			case "prepare":
				_, err = Prepare(tx, "\ufeffCOMMIT", control)
			}
			if !errors.Is(err, ErrArgument) {
				t.Fatalf("transaction SQL was not rejected: %v", err)
			}
			if err := Rollback(tx); err != nil {
				t.Fatalf("managed transaction was ended by rejected SQL: %v", err)
			}
			cursor, err := Query(db, "SELECT count(*) FROM work", nil, nil, control)
			if err != nil {
				t.Fatal(err)
			}
			defer CloseCursor(cursor)
			present, row, err := Next(cursor)
			if err != nil || !present || ValueInteger(RowValues(row)[0]) != 0 {
				t.Fatalf("managed work escaped rollback: %v %v %v", present, row, err)
			}
		})
	}
}

func TestTriggerIdentifiersCannotHideAdditionalStatements(t *testing.T) {
	for _, column := range []string{"case$value", "value$case", "caſe", "end"} {
		t.Run(column, func(t *testing.T) {
			db := testDatabase(t, ":memory:", 25)
			control := testControl(t, 0)
			for _, text := range []string{"CREATE TABLE input(" + column + ")", "CREATE TABLE audit(x)"} {
				if _, _, err := Execute(db, text, nil, nil, control); err != nil {
					t.Fatal(err)
				}
			}
			trigger := "CREATE TRIGGER track AFTER INSERT ON input BEGIN INSERT INTO audit VALUES(NEW." + column + "); END;"
			if _, _, err := Execute(db, trigger+" INSERT INTO audit VALUES(99)", nil, nil, control); !errors.Is(err, ErrArgument) {
				t.Fatalf("additional statement was not rejected: %v", err)
			}
			if _, _, err := Execute(db, trigger, nil, nil, control); err != nil {
				t.Fatalf("valid trigger was rejected or rejected SQL had effects: %v", err)
			}
			if _, _, err := Execute(db, "INSERT INTO input VALUES(7)", nil, nil, control); err != nil {
				t.Fatal(err)
			}
			cursor, err := Query(db, "SELECT x FROM audit", nil, nil, control)
			if err != nil {
				t.Fatal(err)
			}
			defer CloseCursor(cursor)
			present, row, err := Next(cursor)
			if err != nil || !present || ValueInteger(RowValues(row)[0]) != 7 {
				t.Fatalf("valid trigger did not run: %v %v %v", present, row, err)
			}
			if present, _, err := Next(cursor); err != nil || present {
				t.Fatalf("rejected SQL changed audit rows: %v %v", present, err)
			}
		})
	}
}
