// Ported from packages/durable/test/sqlite-facade.test.ts (pi v1.0.0).

package sqlite_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/storage/sqlite"
	"github.com/keejkrej/pi-go/internal/jsonx"
	_ "modernc.org/sqlite"
)

type sftSettlementMode int

const (
	sftModeImmediate sftSettlementMode = iota
	sftModeDelay
	sftModeReject
)

type sftControlledDatabase struct {
	delegate *sqlite.NodeSqliteDatabase

	mu      sync.Mutex
	mode    sftSettlementMode
	started chan struct{}
	release chan struct{}
}

func sftNewControlledDatabase(delegate *sqlite.NodeSqliteDatabase) *sftControlledDatabase {
	return &sftControlledDatabase{delegate: delegate}
}

func (c *sftControlledDatabase) Exec(sqlText string) error { return c.delegate.Exec(sqlText) }
func (c *sftControlledDatabase) Run(sqlText string, params ...sqlite.SqliteValue) error {
	return c.delegate.Run(sqlText, params...)
}
func (c *sftControlledDatabase) Query(sqlText string, params ...sqlite.SqliteValue) (sqlite.SqliteRows, error) {
	return c.delegate.Query(sqlText, params...)
}
func (c *sftControlledDatabase) Close() error { return c.delegate.Close() }

func (c *sftControlledDatabase) ControlNextSettlement(mode sftSettlementMode) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.release != nil {
		panic("A settlement is already pending")
	}
	c.mode = mode
	c.started = make(chan struct{})
	c.release = make(chan struct{})
}

func (c *sftControlledDatabase) WaitStarted(t *testing.T) {
	t.Helper()
	c.mu.Lock()
	started := c.started
	c.mu.Unlock()
	if started == nil {
		t.Fatal("No controlled settlement started")
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("Timed out waiting for controlled transaction to start")
	}
}

func (c *sftControlledDatabase) Settle() {
	c.mu.Lock()
	rel := c.release
	c.release = nil
	c.started = nil
	c.mu.Unlock()
	if rel == nil {
		panic("No settlement is pending")
	}
	close(rel)
}

func (c *sftControlledDatabase) Transaction(callback func(tx sqlite.SqliteExecutor) error) error {
	c.mu.Lock()
	mode := c.mode
	started := c.started
	release := c.release
	c.mode = sftModeImmediate
	c.mu.Unlock()

	if mode == sftModeImmediate {
		return c.delegate.Transaction(callback)
	}
	return c.delegate.Transaction(func(tx sqlite.SqliteExecutor) error {
		err := callback(tx)
		close(started)
		<-release
		if err != nil {
			return err
		}
		if mode == sftModeReject {
			return errors.New("controlled settlement rejection")
		}
		return nil
	})
}

type sftCountingConnector struct {
	baseDriver driver.Driver
	dsn        string

	mu            sync.Mutex
	prepareCounts map[string]int
}

func (c *sftCountingConnector) Connect(_ context.Context) (driver.Conn, error) {
	conn, err := c.baseDriver.Open(c.dsn)
	if err != nil {
		return nil, err
	}
	return &sftCountingConn{Conn: conn, owner: c}, nil
}

func (c *sftCountingConnector) Driver() driver.Driver { return c.baseDriver }

func (c *sftCountingConnector) recordPrepare(query string) {
	c.mu.Lock()
	c.prepareCounts[query]++
	c.mu.Unlock()
}

func (c *sftCountingConnector) repeatedPrepares() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var repeated []string
	for sqlText, count := range c.prepareCounts {
		if count > 1 {
			repeated = append(repeated, sqlText)
		}
	}
	return repeated
}

type sftCountingConn struct {
	driver.Conn
	owner *sftCountingConnector
}

func (c *sftCountingConn) Prepare(query string) (driver.Stmt, error) {
	c.owner.recordPrepare(query)
	return c.Conn.Prepare(query)
}

func (c *sftCountingConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	c.owner.recordPrepare(query)
	if p, ok := c.Conn.(driver.ConnPrepareContext); ok {
		return p.PrepareContext(ctx, query)
	}
	return c.Conn.Prepare(query)
}

func (c *sftCountingConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if e, ok := c.Conn.(driver.ExecerContext); ok {
		return e.ExecContext(ctx, query, args)
	}
	return nil, driver.ErrSkip
}

func (c *sftCountingConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if q, ok := c.Conn.(driver.QueryerContext); ok {
		return q.QueryContext(ctx, query, args)
	}
	return nil, driver.ErrSkip
}

func (c *sftCountingConn) CheckNamedValue(nv *driver.NamedValue) error {
	if chk, ok := c.Conn.(driver.NamedValueChecker); ok {
		return chk.CheckNamedValue(nv)
	}
	return driver.ErrSkip
}

func sftOpenCountingDB(t *testing.T) (*sql.DB, *sftCountingConnector) {
	t.Helper()
	probe, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open probe sqlite failed: %v", err)
	}
	baseDrv := probe.Driver()
	_ = probe.Close()

	connector := &sftCountingConnector{
		baseDriver:    baseDrv,
		dsn:           ":memory:",
		prepareCounts: make(map[string]int),
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(1)
	return db, connector
}

func sftOpenMemoryNodeDB(t *testing.T) *sqlite.NodeSqliteDatabase {
	t.Helper()
	db, err := sqlite.OpenNodeSqliteDatabase(":memory:")
	if err != nil {
		t.Fatalf("OpenNodeSqliteDatabase(:memory:) failed: %v", err)
	}
	return db
}

func TestSqliteFacade(t *testing.T) {
	ctx := chord.BackgroundContext

	t.Run("prepares each storage statement once per connection and reuses it across transactions", func(t *testing.T) {
		rawDB, connector := sftOpenCountingDB(t)
		nodeDB, err := sqlite.NewNodeSqliteDatabase(rawDB)
		if err != nil {
			t.Fatalf("NewNodeSqliteDatabase failed: %v", err)
		}
		storage, err := sqlite.OpenSqliteStorage(nodeDB)
		if err != nil {
			t.Fatalf("OpenSqliteStorage failed: %v", err)
		}

		if _, err := storage.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteConversation{
				Type:  durable.StorageWriteTypeConversation,
				Value: durable.ConversationRecord{Id: durable.RootConversationId},
			},
		}); err != nil {
			t.Fatalf("root commit failed: %v", err)
		}

		batch := make([]durable.StorageWrite, 100)
		for i := range batch {
			batch[i] = durable.StorageWriteEntry{
				Type: durable.StorageWriteTypeEntry,
				Value: durable.EntryRecord{
					Id:             durable.IdFromNumber[durable.EntryId](int64(i + 2)),
					ConversationId: durable.RootConversationId,
					Kind:           "cached",
				},
			}
		}
		if _, err := storage.Commit(ctx, batch); err != nil {
			t.Fatalf("batch commit failed: %v", err)
		}
		if _, err := storage.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteEntry{
				Type: durable.StorageWriteTypeEntry,
				Value: durable.EntryRecord{
					Id:             durable.IdFromNumber[durable.EntryId](102),
					ConversationId: durable.RootConversationId,
					Kind:           "cached-again",
				},
			},
		}); err != nil {
			t.Fatalf("follow-up commit failed: %v", err)
		}

		e2, err := storage.Entry(ctx, durable.IdFromNumber[durable.EntryId](2))
		if err != nil || e2 == nil || e2.Entry.Kind != "cached" {
			t.Fatalf("unexpected entry 2: %+v err=%v", e2, err)
		}
		e102, err := storage.Entry(ctx, durable.IdFromNumber[durable.EntryId](102))
		if err != nil || e102 == nil || e102.Entry.Kind != "cached-again" {
			t.Fatalf("unexpected entry 102: %+v err=%v", e102, err)
		}

		_, err = storage.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteConversation{
				Type:  durable.StorageWriteTypeConversation,
				Value: durable.ConversationRecord{Id: durable.RootConversationId},
			},
		})
		if err == nil || !strings.Contains(err.Error(), "ID 1 already belongs to conversation") {
			t.Fatalf("expected duplicate ID error, got %v", err)
		}

		e2Again, err := storage.Entry(ctx, durable.IdFromNumber[durable.EntryId](2))
		if err != nil || e2Again == nil || e2Again.Entry.Kind != "cached" {
			t.Fatalf("unexpected entry 2 after rollback: %+v err=%v", e2Again, err)
		}
		if repeated := connector.repeatedPrepares(); len(repeated) != 0 {
			t.Fatalf("expected zero repeated prepares, got %v", repeated)
		}
		if err := storage.Close(ctx); err != nil {
			t.Fatalf("close failed: %v", err)
		}
	})

	t.Run("commits work done through the transaction handle and closes idempotently", func(t *testing.T) {
		db := sftOpenMemoryNodeDB(t)
		if err := db.Transaction(func(tx sqlite.SqliteExecutor) error {
			if err := tx.Exec("CREATE TABLE async_probe (value INTEGER)"); err != nil {
				return err
			}
			return tx.Run("INSERT INTO async_probe (value) VALUES (?)", 1)
		}); err != nil {
			t.Fatalf("transaction failed: %v", err)
		}
		rows, err := db.Query("SELECT value FROM async_probe")
		if err != nil {
			t.Fatalf("query failed: %v", err)
		}
		if !rows.Next() {
			_ = rows.Close()
			t.Fatalf("expected 1 row")
		}
		var val int
		if err := rows.Scan(&val); err != nil || val != 1 {
			_ = rows.Close()
			t.Fatalf("expected value=1, got %d err=%v", val, err)
		}
		if err := rows.Close(); err != nil {
			t.Fatalf("rows close failed: %v", err)
		}
		if err := db.Close(); err != nil {
			t.Fatalf("first close failed: %v", err)
		}
		if err := db.Close(); err != nil {
			t.Fatalf("second close failed: %v", err)
		}
	})

	t.Run("serializes concurrent transactions", func(t *testing.T) {
		db := sftOpenMemoryNodeDB(t)
		defer func() { _ = db.Close() }()
		if err := db.Exec("CREATE TABLE transaction_queue (value INTEGER)"); err != nil {
			t.Fatalf("create table failed: %v", err)
		}

		firstStarted := make(chan struct{})
		releaseFirst := make(chan struct{})
		firstErrCh := make(chan error, 1)
		go func() {
			firstErrCh <- db.Transaction(func(tx sqlite.SqliteExecutor) error {
				if err := tx.Exec("INSERT INTO transaction_queue (value) VALUES (1)"); err != nil {
					return err
				}
				close(firstStarted)
				<-releaseFirst
				return nil
			})
		}()
		<-firstStarted

		var secondStarted atomic.Bool
		secondErrCh := make(chan error, 1)
		go func() {
			secondErrCh <- db.Transaction(func(tx sqlite.SqliteExecutor) error {
				secondStarted.Store(true)
				return tx.Exec("INSERT INTO transaction_queue (value) VALUES (2)")
			})
		}()

		time.Sleep(25 * time.Millisecond)
		if secondStarted.Load() {
			t.Fatalf("expected second transaction to wait while first transaction holds lock")
		}

		close(releaseFirst)
		if err := <-firstErrCh; err != nil {
			t.Fatalf("first transaction failed: %v", err)
		}
		if err := <-secondErrCh; err != nil {
			t.Fatalf("second transaction failed: %v", err)
		}

		rows, err := db.Query("SELECT value FROM transaction_queue ORDER BY value")
		if err != nil {
			t.Fatalf("query failed: %v", err)
		}
		defer func() { _ = rows.Close() }()
		var values []int
		for rows.Next() {
			var v int
			if err := rows.Scan(&v); err != nil {
				t.Fatalf("scan failed: %v", err)
			}
			values = append(values, v)
		}
		if len(values) != 2 || values[0] != 1 || values[1] != 2 {
			t.Fatalf("expected [1, 2], got %v", values)
		}
	})

	t.Run("queues ordinary operations behind an active transaction and keeps rollback isolated", func(t *testing.T) {
		db := sftOpenMemoryNodeDB(t)
		defer func() { _ = db.Close() }()
		if err := db.Exec("CREATE TABLE operation_queue (value INTEGER)"); err != nil {
			t.Fatalf("create table failed: %v", err)
		}

		txStarted := make(chan struct{})
		releaseTx := make(chan struct{})
		txErrCh := make(chan error, 1)
		go func() {
			txErrCh <- db.Transaction(func(tx sqlite.SqliteExecutor) error {
				if err := tx.Run("INSERT INTO operation_queue (value) VALUES (?)", 1); err != nil {
					return err
				}
				close(txStarted)
				<-releaseTx
				return errors.New("roll back")
			})
		}()
		<-txStarted

		var outsideDone atomic.Bool
		outsideErrCh := make(chan error, 1)
		go func() {
			err := db.Run("INSERT INTO operation_queue (value) VALUES (?)", 2)
			outsideDone.Store(true)
			outsideErrCh <- err
		}()

		time.Sleep(25 * time.Millisecond)
		if outsideDone.Load() {
			t.Fatalf("expected outside write to wait behind active transaction")
		}

		close(releaseTx)
		if err := <-txErrCh; err == nil || !strings.Contains(err.Error(), "roll back") {
			t.Fatalf("expected rollback error, got %v", err)
		}
		if err := <-outsideErrCh; err != nil {
			t.Fatalf("outside write failed: %v", err)
		}

		rows, err := db.Query("SELECT value FROM operation_queue ORDER BY value")
		if err != nil {
			t.Fatalf("query failed: %v", err)
		}
		defer func() { _ = rows.Close() }()
		var values []int
		for rows.Next() {
			var v int
			if err := rows.Scan(&v); err != nil {
				t.Fatalf("scan failed: %v", err)
			}
			values = append(values, v)
		}
		if len(values) != 1 || values[0] != 2 {
			t.Fatalf("expected [2], got %v", values)
		}
	})

	t.Run("queues concurrent database calls spawned while a transaction runs", func(t *testing.T) {
		db := sftOpenMemoryNodeDB(t)
		defer func() { _ = db.Close() }()
		if err := db.Exec("CREATE TABLE barrier_probe (value INTEGER)"); err != nil {
			t.Fatalf("create table failed: %v", err)
		}

		outsideErrCh := make(chan error, 1)
		txErr := db.Transaction(func(tx sqlite.SqliteExecutor) error {
			go func() {
				outsideErrCh <- db.Run("INSERT INTO barrier_probe (value) VALUES (?)", 2)
			}()
			time.Sleep(15 * time.Millisecond)
			if err := tx.Run("INSERT INTO barrier_probe (value) VALUES (?)", 1); err != nil {
				return err
			}
			return errors.New("roll back")
		})
		if txErr == nil || !strings.Contains(txErr.Error(), "roll back") {
			t.Fatalf("expected roll back error, got %v", txErr)
		}
		if err := <-outsideErrCh; err != nil {
			t.Fatalf("outside write failed: %v", err)
		}
		rows, err := db.Query("SELECT value FROM barrier_probe")
		if err != nil {
			t.Fatalf("query failed: %v", err)
		}
		defer func() { _ = rows.Close() }()
		var values []int
		for rows.Next() {
			var v int
			if err := rows.Scan(&v); err != nil {
				t.Fatalf("scan failed: %v", err)
			}
			values = append(values, v)
		}
		if len(values) != 1 || values[0] != 2 {
			t.Fatalf("expected [2], got %v", values)
		}
	})

	t.Run("lets admitted reads complete around Close and rejects post-close calls", func(t *testing.T) {
		storage, err := sqlite.OpenSqliteStorage(sftOpenMemoryNodeDB(t))
		if err != nil {
			t.Fatalf("open failed: %v", err)
		}
		entryId := durable.IdFromNumber[durable.EntryId](2)
		if _, err := storage.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteConversation{
				Type:  durable.StorageWriteTypeConversation,
				Value: durable.ConversationRecord{Id: durable.RootConversationId},
			},
			durable.StorageWriteEntry{
				Type:  durable.StorageWriteTypeEntry,
				Value: durable.EntryRecord{Id: entryId, ConversationId: durable.RootConversationId, Kind: "probe"},
			},
		}); err != nil {
			t.Fatalf("commit failed: %v", err)
		}

		page, err := storage.ScanEntries(ctx, durable.EntryQuery{ConversationId: durable.RootConversationId}, 10, nil)
		if err != nil || len(page.Items) != 1 || page.Items[0].Id != entryId {
			t.Fatalf("scan failed: %+v err=%v", page, err)
		}
		got, err := storage.EntryInConversation(ctx, durable.RootConversationId, entryId)
		if err != nil || got == nil || got.Entry.Kind != "probe" {
			t.Fatalf("entry lookup failed: %+v err=%v", got, err)
		}
		head, err := storage.FindLatestHeadMarker(ctx, durable.RootConversationId, nil)
		if err != nil || head != nil {
			t.Fatalf("head lookup failed: %+v err=%v", head, err)
		}
		if err := storage.Close(ctx); err != nil {
			t.Fatalf("close failed: %v", err)
		}
		if err := storage.Close(ctx); err != nil {
			t.Fatalf("repeat close failed: %v", err)
		}
		_, err = storage.ScanEntries(ctx, durable.EntryQuery{ConversationId: durable.RootConversationId}, 10, nil)
		if err == nil || !strings.Contains(err.Error(), "SqliteStorage is closed") {
			t.Fatalf("expected closed error, got %v", err)
		}
	})

	t.Run("rejects a transaction handle used after its transaction settles", func(t *testing.T) {
		db := sftOpenMemoryNodeDB(t)
		defer func() { _ = db.Close() }()
		if err := db.Exec("CREATE TABLE stale_probe (value INTEGER)"); err != nil {
			t.Fatalf("create table failed: %v", err)
		}
		var handle sqlite.SqliteExecutor
		if err := db.Transaction(func(tx sqlite.SqliteExecutor) error {
			handle = tx
			return tx.Run("INSERT INTO stale_probe (value) VALUES (?)", 1)
		}); err != nil {
			t.Fatalf("transaction failed: %v", err)
		}
		const stale = "SQLite transaction handle is no longer active"
		if err := handle.Exec("INSERT INTO stale_probe (value) VALUES (2)"); err == nil || !strings.Contains(err.Error(), stale) {
			t.Fatalf("expected stale exec error, got %v", err)
		}
		if err := handle.Run("INSERT INTO stale_probe (value) VALUES (?)", 3); err == nil || !strings.Contains(err.Error(), stale) {
			t.Fatalf("expected stale run error, got %v", err)
		}
		rows, err := db.Query("SELECT value FROM stale_probe")
		if err != nil {
			t.Fatalf("query failed: %v", err)
		}
		defer func() { _ = rows.Close() }()
		var count int
		for rows.Next() {
			count++
		}
		if count != 1 {
			t.Fatalf("expected 1 row, got %d", count)
		}
	})

	t.Run("does not preserve a guaranteed rejection when rollback itself fails", func(t *testing.T) {
		db := sftOpenMemoryNodeDB(t)
		defer func() { _ = db.Close() }()
		if err := db.Exec("CREATE TABLE rollback_probe (value INTEGER)"); err != nil {
			t.Fatalf("create table failed: %v", err)
		}
		err := db.Transaction(func(tx sqlite.SqliteExecutor) error {
			if err := tx.Exec("INSERT INTO rollback_probe (value) VALUES (1)"); err != nil {
				return err
			}
			if err := tx.Exec("COMMIT"); err != nil {
				return err
			}
			return durable.NewStorageRejected("rejected after an escaped commit", nil)
		})
		if err == nil || !strings.Contains(err.Error(), "rollback failed") || !strings.Contains(err.Error(), "rejected after an escaped commit") {
			t.Fatalf("expected combined rollback-failed error wrapping callback error, got %v", err)
		}
		rows, qErr := db.Query("SELECT value FROM rollback_probe")
		if qErr != nil {
			t.Fatalf("query failed: %v", qErr)
		}
		defer func() { _ = rows.Close() }()
		if !rows.Next() {
			t.Fatalf("expected escaped commit row")
		}
	})

	t.Run("awaits transaction settlement and adopts IDs only after success", func(t *testing.T) {
		db := sftNewControlledDatabase(sftOpenMemoryNodeDB(t))

		db.ControlNextSettlement(sftModeDelay)
		type openRes struct {
			storage *sqlite.SqliteStorage
			err     error
		}
		openCh := make(chan openRes, 1)
		go func() {
			st, err := sqlite.OpenSqliteStorage(db)
			openCh <- openRes{storage: st, err: err}
		}()
		db.WaitStarted(t)
		select {
		case res := <-openCh:
			t.Fatalf("expected open to await transaction settlement, finished early: %+v", res)
		default:
		}
		db.Settle()
		opened := <-openCh
		if opened.err != nil {
			t.Fatalf("open failed: %v", opened.err)
		}
		storage := opened.storage
		defer func() { _ = storage.Close(ctx) }()

		db.ControlNextSettlement(sftModeDelay)
		type commitRes struct {
			seq durable.Seq
			err error
		}
		commitCh := make(chan commitRes, 1)
		go func() {
			seq, err := storage.Commit(ctx, []durable.StorageWrite{
				durable.StorageWriteEntry{
					Type: durable.StorageWriteTypeEntry,
					Value: durable.EntryRecord{
						Id:             durable.IdFromNumber[durable.EntryId](100),
						ConversationId: durable.RootConversationId,
						Kind:           "settled",
					},
				},
			})
			commitCh <- commitRes{seq: seq, err: err}
		}()
		db.WaitStarted(t)
		select {
		case res := <-commitCh:
			t.Fatalf("expected commit to wait settlement, got %+v", res)
		default:
		}
		mintedBefore, err := storage.MintId()
		if err != nil || mintedBefore != 2 {
			t.Fatalf("expected mintedBefore=2 while commit un-settled, got %d err=%v", mintedBefore, err)
		}
		db.Settle()
		c1 := <-commitCh
		if c1.err != nil || c1.seq != 1 {
			t.Fatalf("expected seq 1, got %+v", c1)
		}
		mintedAfter, err := storage.MintId()
		if err != nil || mintedAfter != 101 {
			t.Fatalf("expected mintedAfter=101, got %d err=%v", mintedAfter, err)
		}

		db.ControlNextSettlement(sftModeReject)
		rejectCh := make(chan commitRes, 1)
		go func() {
			seq, err := storage.Commit(ctx, []durable.StorageWrite{
				durable.StorageWriteEntry{
					Type: durable.StorageWriteTypeEntry,
					Value: durable.EntryRecord{
						Id:             durable.IdFromNumber[durable.EntryId](200),
						ConversationId: durable.RootConversationId,
						Kind:           "rejected",
					},
				},
			})
			rejectCh <- commitRes{seq: seq, err: err}
		}()
		db.WaitStarted(t)
		mintedDuringReject, err := storage.MintId()
		if err != nil || mintedDuringReject != 102 {
			t.Fatalf("expected mintedDuringReject=102, got %d err=%v", mintedDuringReject, err)
		}
		db.Settle()
		c2 := <-rejectCh
		if c2.err == nil || !strings.Contains(c2.err.Error(), "controlled settlement rejection") {
			t.Fatalf("expected rejection error, got %+v", c2)
		}
		mintedAfterReject, err := storage.MintId()
		if err != nil || mintedAfterReject != 103 {
			t.Fatalf("expected mintedAfterReject=103, got %d err=%v", mintedAfterReject, err)
		}
		missing, err := storage.Entry(ctx, durable.IdFromNumber[durable.EntryId](200))
		if err != nil || missing != nil {
			t.Fatalf("expected rolled-back entry 200 to be nil, got %+v err=%v", missing, err)
		}
	})

	t.Run("reads a document from one committed state while a commit replaces its base", func(t *testing.T) {
		docId := durable.IdFromNumber[durable.DocumentId](5)
		for iter := 0; iter < 16; iter++ {
			storage, err := sqlite.OpenSqliteStorage(sftOpenMemoryNodeDB(t))
			if err != nil {
				t.Fatalf("open failed: %v", err)
			}
			if _, err := storage.Commit(ctx, []durable.StorageWrite{
				durable.StorageWriteDocumentCreate{
					Type: durable.StorageWriteTypeDocumentCreate,
					Record: durable.DocumentCreate{
						Id:    docId,
						Kind:  "replaced",
						Scope: durable.DocumentScope{Kind: durable.DocumentScopeSession},
					},
					Content: durable.DocumentContent{
						Kind:    durable.DocumentContentBase,
						Version: 1,
						Value:   jsonx.ObjectOf("value", float64(1)),
					},
				},
			}); err != nil {
				t.Fatalf("create failed: %v", err)
			}

			var wg sync.WaitGroup
			var readDoc *durable.StoredDocument
			var readErr, replaceErr error
			wg.Add(2)
			go func() {
				defer wg.Done()
				readDoc, readErr = storage.Document(ctx, docId, durable.DocumentPointCurrent())
			}()
			go func() {
				defer wg.Done()
				_, replaceErr = storage.Commit(ctx, []durable.StorageWrite{
					durable.StorageWriteDocumentChange{
						Type: durable.StorageWriteTypeDocumentChange,
						Id:   docId,
						Content: durable.DocumentContent{
							Kind:    durable.DocumentContentBase,
							Version: 1,
							Value:   jsonx.ObjectOf("value", float64(2)),
						},
					},
				})
			}()
			wg.Wait()
			if readErr != nil || replaceErr != nil || readDoc == nil {
				t.Fatalf("concurrent read/replace failed: read=%v replace=%v doc=%v", readErr, replaceErr, readDoc)
			}
			v1 := jsonx.DeepEqual(readDoc.Value, jsonx.ObjectOf("value", float64(1)))
			v2 := jsonx.DeepEqual(readDoc.Value, jsonx.ObjectOf("value", float64(2)))
			if !v1 && !v2 {
				t.Fatalf("expected {value:1} or {value:2}, got %#v", readDoc.Value)
			}
			_ = storage.Close(ctx)
		}
	})
}
