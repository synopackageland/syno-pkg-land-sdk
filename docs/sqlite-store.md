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

Open the database in a dedicated private directory under package data. The
Store calls `sqlitefile.PrepareDir` before creating the file and applies
`sqlitefile.Configure` to the writer. New databases are created with mode
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
| Pending operations | 128 | `QueueSize` |
| Work per transaction | 32 callbacks and 1 MiB in declared weights | `MaxBatchItems`, `MaxBatchBytes` |

Both read pools use `mode=ro` and set `query_only=ON` on each connection.
Use `BackgroundReader` for long scans when you enable it. Close rows and
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

## Run maintenance

Schedule `Store.Maintain(ctx)` from your App when the database is quiet. The
writer runs it outside the group transaction using the [SQLite file
maintenance defaults](sqlite-file-layout.md#schedule-upkeep). An App that
needs incremental vacuum should set its SQLite auto-vacuum mode as part of
its database preparation before creating tables.

See [SQLite transactions](https://www.sqlite.org/lang_transaction.html),
[SQLite WAL](https://www.sqlite.org/wal.html), and
[SQLite pragmas](https://www.sqlite.org/pragma.html).
