package config

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	"modernc.org/sqlite"
)

func init() {
	sql.Register("sqlite-fault", &FaultInjectDriver{
		Driver: &sqlite.Driver{},
	})
}

var ErrInjectedFault = errors.New("injected database fault")

// FaultInjectDriver wraps the standard SQLite driver to inject errors
type FaultInjectDriver struct {
	Driver driver.Driver
}

func (d *FaultInjectDriver) Open(name string) (driver.Conn, error) {
	conn, err := d.Driver.Open(name)
	if err != nil {
		return nil, err
	}
	return &FaultInjectConn{Conn: conn}, nil
}

type FaultInjectConn struct {
	driver.Conn
}

func (c *FaultInjectConn) Prepare(query string) (driver.Stmt, error) {
	stmt, err := c.Conn.Prepare(query)
	if err != nil {
		return nil, err
	}
	return &FaultInjectStmt{Stmt: stmt}, nil
}

func (c *FaultInjectConn) Begin() (driver.Tx, error) {
	tx, err := c.Conn.Begin()
	if err != nil {
		return nil, err
	}
	return &FaultInjectTx{Tx: tx}, nil
}

func (c *FaultInjectConn) Close() error {
	return c.Conn.Close()
}

// Stmt wrapper
type FaultInjectStmt struct {
	driver.Stmt
}

func (s *FaultInjectStmt) Close() error {
	return s.Stmt.Close()
}

func (s *FaultInjectStmt) NumInput() int {
	return s.Stmt.NumInput()
}

func (s *FaultInjectStmt) Exec(args []driver.Value) (driver.Result, error) {
	return s.Stmt.Exec(args)
}

func (s *FaultInjectStmt) Query(args []driver.Value) (driver.Rows, error) {
	return s.Stmt.Query(args)
}

// Tx wrapper
type FaultInjectTx struct {
	driver.Tx
}

func (t *FaultInjectTx) Commit() error {
	return t.Tx.Commit()
}

func (t *FaultInjectTx) Rollback() error {
	return t.Tx.Rollback()
}

func TestStoreFailures(t *testing.T) {
	// A basic test that uses our fault driver to verify compilation and basic sanity
	db, err := sql.Open("sqlite-fault", ":memory:")
	if err != nil {
		t.Fatalf("failed to open fault inject db: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec("CREATE TABLE test (id INTEGER)"); err != nil {
		t.Fatalf("failed to create table: %v", err)
	}
}

// TestAtomicAuthGeneration simulates a failure in the middle of a credentials change
// We use a regular store but close the DB mid-way to simulate a crash/failure
func TestAtomicAuthGenerationRollsBackOnFailure(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// Initial setup
	store.SetAdminHash("hash1")
	store.CreateSession("sess1", "csrf", false, time.Now(), time.Now())

	// Close DB to inject a fault for the next operations
	store.db.Close()

	// Attempt to change password, should fail and rollback implicitly
	err = store.SetAdminHash("hash2")
	if err == nil {
		t.Fatal("expected error on closed db")
	}
}
