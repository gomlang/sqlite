package adapter

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"

	"modernc.org/sqlite"
)

var errRollbackInjected = errors.New("injected rollback execution failure")

type rollbackFault struct {
	command string
	after   bool
}

type rollbackConnector struct {
	native sqlite.Driver
	fault  *rollbackFault
}

func (c *rollbackConnector) Driver() driver.Driver { return &c.native }
func (c *rollbackConnector) Connect(context.Context) (driver.Conn, error) {
	conn, err := c.native.Open(":memory:")
	if err != nil {
		return nil, err
	}
	return &rollbackConnection{Conn: conn, fault: c.fault}, nil
}

type rollbackConnection struct {
	driver.Conn
	fault *rollbackFault
}

func (c *rollbackConnection) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	native := c.Conn.(driver.ExecerContext)
	if query != c.fault.command {
		return native.ExecContext(ctx, query, args)
	}
	c.fault.command = ""
	if c.fault.after {
		if _, err := native.ExecContext(ctx, query, args); err != nil {
			return nil, err
		}
	}
	return nil, errRollbackInjected
}

func rollbackDatabase(t *testing.T) (Session, *rollbackFault) {
	t.Helper()
	db := testDatabase(t, ":memory:", 25)
	fault := &rollbackFault{}
	pool := sql.OpenDB(&rollbackConnector{fault: fault})
	conn, err := pool.Conn(context.Background())
	if err != nil {
		pool.Close()
		t.Fatal(err)
	}
	// Keep the adapter's ordinary lifecycle while injecting a single execution
	// failure around the real SQLite driver's otherwise unchanged connection.
	state := db.(*SessionState).db
	state.conn.Close()
	state.pool.Close()
	state.conn, state.pool = conn, pool
	return db, fault
}

func assertNoRolledBackRows(t *testing.T, db Session, control Control) {
	t.Helper()
	cursor, err := Query(db, "SELECT count(*) FROM work", nil, nil, control)
	if err != nil {
		t.Fatal(err)
	}
	defer CloseCursor(cursor)
	present, row, err := Next(cursor)
	if err != nil || !present || ValueInteger(RowValues(row)[0]) != 0 {
		t.Fatalf("rolled-back changes survived: %v %v %v", present, row, err)
	}
}

func TestFailedRollbackRetainsManagedTransaction(t *testing.T) {
	for _, nested := range []bool{false, true} {
		name := "root"
		if nested {
			name = "savepoint"
		}
		t.Run(name, func(t *testing.T) {
			db, fault := rollbackDatabase(t)
			control := testControl(t, 0)
			if _, _, err := Execute(db, "CREATE TABLE work(x)", nil, nil, control); err != nil {
				t.Fatal(err)
			}
			root, err := Begin(db, 0, control)
			if err != nil {
				t.Fatal(err)
			}
			target := root
			if nested {
				target, err = Savepoint(root, control)
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, _, err := Execute(target, "INSERT INTO work VALUES(7)", nil, nil, control); err != nil {
				t.Fatal(err)
			}
			fault.command = "ROLLBACK"
			if nested {
				fault.command = "ROLLBACK TO SAVEPOINT " + target.(*SessionState).name
			}
			if err := Rollback(target); !errors.Is(err, errRollbackInjected) {
				t.Fatalf("missing original rollback failure: %v", err)
			}
			if nested {
				if err := Commit(root, control); !errors.Is(err, ErrBusy) {
					t.Fatalf("parent could commit changes after failed child rollback: %v", err)
				}
			} else if _, _, err := Execute(db, "INSERT INTO work VALUES(99)", nil, nil, control); !errors.Is(err, ErrBusy) {
				t.Fatalf("database could execute inside an unmanaged transaction: %v", err)
			}
			if IsClosed(target) {
				t.Fatal("active native transaction handle was invalidated")
			}
			if err := Rollback(target); err != nil {
				t.Fatalf("rollback retry failed: %v", err)
			}
			if nested {
				if err := Commit(root, control); err != nil {
					t.Fatal(err)
				}
			}
			assertNoRolledBackRows(t, db, control)
		})
	}
}

func TestRollbackFailureAfterNativeCompletionInvalidatesTransaction(t *testing.T) {
	db, fault := rollbackDatabase(t)
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
	fault.command, fault.after = "ROLLBACK", true
	if err := Rollback(tx); !errors.Is(err, errRollbackInjected) {
		t.Fatalf("missing rollback completion failure: %v", err)
	}
	if !IsClosed(tx) {
		t.Fatal("ended native transaction retained a live handle")
	}
	assertNoRolledBackRows(t, db, control)
}
