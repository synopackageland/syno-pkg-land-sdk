# SQLite stores for Go Apps

Use `github.com/synopackageland/syno-pkg-land-sdk/sqlitestore` for App-owned
SQLite files. Each Store owns one writable connection and one interactive
read pool. You can enable a second read pool for long scans so they cannot
occupy the interactive pool's connections.

Register a `database/sql` SQLite driver that supports SQLite file URIs. For
example, import `_ "modernc.org/sqlite"` and set `DriverName: "sqlite"`:

```go
store, err := sqlitestore.Open(ctx, filepath.Join(dataDir, "db", "app.db"),
    sqlitestore.Options{DriverName: "sqlite", BackgroundReaders: 1})
if err != nil {
    return err
}
defer store.Close()

if err := store.Write(ctx, func(ctx context.Context, tx sqlitestore.Tx) error {
    _, err := tx.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS notes (" +
        "id INTEGER PRIMARY KEY, body TEXT NOT NULL)")
    return err
}); err != nil {
    return err
}

if err := store.WriteWeighted(ctx, len(body), func(ctx context.Context, tx sqlitestore.Tx) error {
    _, err := tx.ExecContext(ctx, "INSERT INTO notes(body) VALUES (?)", body)
    return err
}); err != nil {
    return err
}

var count int
return store.Reader.QueryRowContext(ctx, "SELECT count(*) FROM notes").Scan(&count)
```

Open the database in a dedicated private directory under package data. When
`Open` creates that directory it calls `sqlitefile.PrepareDir`; files created
later in it inherit the directory's layout. The writer gets
`sqlitefile.ConfigureWith` with `JournalSizeLimit` (default 64 MiB). New databases are created with mode
`0600`. Apps own schema migrations and decide whether to [convert existing
files to NoCOW](sqlite-file-layout.md#convert-an-existing-database) under an
exclusive maintenance lock.

## Pool and transaction settings

| Setting | Default | Override |
| --- | --- | --- |
| Journal mode | WAL | Required by the Store |
| Commit sync | `NORMAL` | `Durability: sqlitestore.Full` for commits that must survive power loss |
| Busy timeout | 5,000 ms per connection | `BusyTimeoutMS`, or `-1` to disable SQLite lock waiting |
| Foreign keys | On | `DisableForeignKeys` |
| Temporary storage | File | `TempStoreMemory` |
| Trusted schema | Off | `AllowTrustedSchema` |
| Cache per connection | 2,048 KiB writer, 512 KiB interactive, 1,024 KiB background | `WriterCacheKiB`, `ReaderCacheKiB`, `BackgroundCacheKiB` |
| Read connections | 2 interactive, background pool disabled | `Readers`, `BackgroundReaders` |
| Extra interactive connections under load | 0 | `OverflowReaders`; they close when released while `Readers` connections are idle |
| Retained WAL size | 64 MiB | `JournalSizeLimit` |
| Per-connection setup | None | `ConnectionInit` statements on every connection; `DSNParams` on every file URI |
| Pending operations | 128 per priority | `QueueSize` |
| Work per transaction | 32 callbacks and 1 MiB in declared weights | `MaxBatchItems`, `MaxBatchBytes` |

Both read pools use `mode=ro` and set `query_only=ON` on each connection.
Use `BackgroundReader` for long scans when you enable it. When another
workload needs its own connections, `Store.OpenReader(ctx,
sqlitestore.ReaderOptions{Conns: n})` opens one more pool with the same
settings; the Store closes it too. Close rows and
read transactions promptly so WAL checkpoints can advance. The Store owns
the pools and their configuration. Use them for reads and let `Store.Close`
close them.

## Write safely

The writer starts `BEGIN IMMEDIATE` and groups work already in the queue.
An idle writer starts immediately. `WriteWeighted` uses an App-supplied byte
estimate. An oversized callback runs alone, so split large imports into
bounded pieces. `Write` uses the item limit without a byte estimate.

A callback runs in its own savepoint. When it returns an error or panics, its
work is rolled back and the other callbacks can commit. The panic is
re-raised in the caller after rollback. Success is reported after the common
commit. If SQLite loses the transaction or the commit fails, callers whose
work depended on that commit receive an error. Callbacks are never replayed.

A full queue waits for space or context cancellation. Cancellation removes a
queued operation or skips it before its callback starts. A running callback
receives the caller's context for Go work. Its SQL runs without cancellation
because a SQLite interrupt can roll back the entire group. If cancellation is
observed before the callback's savepoint is released, the writer rolls the
item back. After that boundary the caller receives the commit outcome.

Use the supplied transaction for all writes and any reads that need to see
those writes. Return promptly on SQL errors and finish reading or close all
rows before returning. Leave transaction control and connection pragmas to the
Store, and avoid SQLite conflict clauses that roll back the whole transaction.
Keep callbacks free of external side effects and recursive Store operations.
`Close` stops admission, waits for accepted operations, then closes all pools.

The queue and group commit live within one Store in one process. Share one
Store per database in a long-running App. With CGI processes, SQLite's WAL
and busy handler arbitrate between processes. Size the write callbacks and
busy timeout for that workload.

## Attach other files and add connection settings

`ConnectionInit` runs on every connection, writer and readers, after the
Store's own settings and before readers turn on `query_only`. Use it for an
`ATTACH` (with `?mode=ro` in the attached file's URI for a read-only
attachment) or App pragmas:

```go
catalog := url.URL{Scheme: "file", Path: catalogPath, RawQuery: "mode=ro"}
opts := sqlitestore.Options{DriverName: "sqlite",
    ConnectionInit: []string{"ATTACH DATABASE '" + catalog.String() + "' AS g"}}
```

Quote paths as SQL string literals. `DSNParams` adds driver URI parameters
to every connection; the Store sets `mode` itself. For per-connection state
that needs Go, such as TEMP triggers on the writer, use `ConfigureWriter`.

## Choose priority and durability per write

`WriteWith` takes `sqlitestore.WriteOptions`:

```go
err := store.WriteWith(ctx, sqlitestore.WriteOptions{
    Priority:   sqlitestore.Urgent,
    Durability: sqlitestore.Full,
}, renewLease)
```

| Option | Meaning |
| --- | --- |
| `Priority: Interactive` (default) | Work a caller waits for |
| `Priority: Urgent` | Runs before every other waiting operation, at the next transaction boundary. Keep it for small writes that keep the App alive, such as a lease renewal |
| `Priority: Background` | Bulk and periodic work. While Interactive and Background work both wait, the writer alternates between them, so neither starves and an Interactive write waits for at most one Background transaction |
| `Durability` | The commit uses the stronger of this and the Store's `Durability`. A transaction never mixes durabilities; the writer switches `synchronous` between transactions on its one connection |
| `Bytes` | The estimated cost, as in `WriteWeighted` |
| `Interruptible` | The write runs in its own transaction and its SQL follows the caller's context, so cancellation interrupts a running statement. It rolls back only this write |

A running callback is never interrupted by a higher priority. Each priority
has its own `QueueSize`, so a full Background queue never blocks an Urgent
write.

## Write with `*sql.Tx`

Code written against `database/sql` transactions can use `WriteTx`:

```go
err := store.WriteTx(ctx, sqlitestore.WriteOptions{}, func(ctx context.Context, tx *sql.Tx) error {
    _, err := tx.ExecContext(ctx, "UPDATE notes SET body = ? WHERE id = ?", body, id)
    return err
})
```

The transaction runs alone on the writer, in priority order with other
writes, and begins `IMMEDIATE`. With driver names `sqlite` (modernc) and
`sqlite3` (mattn) the Store sets `_txlock=immediate` on the writer; for other
drivers set the equivalent in `DSNParams`. It follows the caller's context:
cancellation rolls it back. Return nil to commit; do not call `Commit` or
`Rollback` yourself.

## Take turns across database files

Stores that share a `sqlitestore.NewTurn()` in their `Options.Turn` never
run transactions or exclusive work at the same time; their writers take
turns in arrival order. Use it when an App keeps several database files on
one volume and wants one write stream at a time. Each Store still commits
its own file; a Turn does not make a write span files atomically.

## React to a commit

Inside a write callback, `sqlitestore.AfterCommit(ctx, fn)` registers a
function that runs after that callback's transaction commits, in order,
before any caller in the transaction returns and before the next transaction
starts. It is dropped when the callback's work rolls back or the commit
fails. Use it to publish a change, advance an in-memory sequence or wake a
reader. Keep it short and do not use the Store from it.

`Options.ConfigureWriter` runs on every new physical writer connection,
including a replacement after a failure. Use it for per-connection state
such as TEMP triggers or functions that callbacks rely on.

## Wait for other processes

When another process holds SQLite's write lock past the busy timeout, the
writer retries `BEGIN IMMEDIATE` without starting any callback, until the
lock is free or every caller in the waiting group cancels. A busy `COMMIT`
keeps its transaction and is retried. Callbacks are never replayed.

By default the writer waits 25 ms between attempts and never gives up. Set
`Options.BusyRetry` to choose the delay per attempt or to give up; it
receives a `BusyAttempt` with the statement, attempt number and error. When
it gives up, the callers receive the busy error.

## Observe writes

`Options.Observe` receives `sqlitestore.WriteStats` for each write that
reached a transaction: queue wait, the group's `BEGIN`, callback and
`COMMIT` durations, the commit's start and end time, group size, priority,
durability and whether the write failed. It runs on the caller's goroutine
after the outcome and must not block. `Store.Pending()` reports how many
operations wait for the writer. `WriteOptions.Observe` receives the
same statistics for one write, after `Options.Observe`. Statistics contain
no SQL or values.

## Use the writer connection directly

`Store.Exclusive(ctx, fn)` runs `fn` on the writer connection outside any
transaction, between transactions. Use it for migrations that control their
own transaction, `VACUUM`, `VACUUM INTO` or a checkpoint. `fn` gets the
caller's context. Afterwards the Store rolls back any transaction `fn` left
open and restores its connection settings.

The writer keeps up to 256 prepared single statements per connection, so
repeated callbacks do not compile their SQL again.

## Run maintenance

Schedule `Store.Maintain(ctx)` from your App when the database is quiet.
`Store.MaintainWith(ctx, opts)` takes `sqlitefile.MaintainOptions` to set the
optimize mask, ANALYZE limit and pages reclaimed per call. The
writer runs it outside the group transaction using the [SQLite file
maintenance defaults](sqlite-file-layout.md#schedule-upkeep). An App that
needs incremental vacuum should set its SQLite auto-vacuum mode as part of
its database preparation before creating tables.

See [SQLite transactions](https://www.sqlite.org/lang_transaction.html),
[SQLite WAL](https://www.sqlite.org/wal.html), and
[SQLite pragmas](https://www.sqlite.org/pragma.html).
