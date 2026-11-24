# SQLite file layout and maintenance

Use the Go package `github.com/synopackageland/syno-pkg-land-sdk/sqlitefile`
for App-owned databases opened through `database/sql`. The App supplies its
SQLite driver and decides when to schedule maintenance.

## Prepare the database directory

Call `sqlitefile.PrepareDir(dbDir)` before opening any database in a dedicated
App data directory. It creates missing directories with mode `0700`. On Btrfs
it sets the directory's NoCOW flag so new main databases, WALs, shared-memory
files and rollback journals inherit it. Existing files keep their own flags.
An explicit compression flag or Btrfs compression property on the directory
takes precedence. A failed filesystem optimization is logged once per process.
A failure to create the directory is returned to the App.

Set `SYNO_SQLITE_NOCOW=0` in the App environment to skip preparation of the
NoCOW flag and conversion. The setting leaves existing inode flags in place.
Use `sqlitefile.State(dbDir)` for directory health and
`sqlitefile.State(dbPath)` to inspect a specific file. It reports `on` for a
set NoCOW flag, `off` for a clear flag, and `unsupported` when the flag cannot
be probed, including on other filesystems.

On Btrfs, NoCOW data loses Btrfs data checksums and transparent compression.
Keep the SQLite journaling and sync policy appropriate to the App. For example,
use `synchronous=FULL` for WAL commits that must survive a power loss. Backups
and periodic SQLite checks still matter. See [Btrfs file attributes] and [SQLite
WAL].

## Convert an existing database

Call `ConvertToNoCOW` at startup before opening the normal pool or during an
exclusive maintenance window. Hold an App-level exclusive lock across the call.
Every reader and writer must be disconnected, including backup or auxiliary
processes that use the file. Run as the package account that owns the file. Use
a private, App-controlled database directory and reserve names beginning with
`.sqlitefile-` there for the converter.

For example, an App using modernc SQLite can run this code under its startup
lock before opening its normal pool:

```go
if err := sqlitefile.PrepareDir(dbDir); err != nil {
    return err
}
changed, err := sqlitefile.ConvertToNoCOW(ctx, filepath.Join(dbDir, "main.db"),
    func(path string) (*sql.DB, error) {
        dsn := url.URL{Scheme: "file", Path: path, RawQuery: "mode=rw"}
        return sql.Open("sqlite", dsn.String())
    })
if err != nil {
    return fmt.Errorf("prepare SQLite storage (replaced=%t): %w", changed, err)
}
// Open the App's usual pool and apply its normal pragmas.
```

The maintenance opener uses the exact path supplied by the SDK, opens an
existing file read/write and returns a fresh pool for the SDK to close. The App
should register any extensions the schema requires. Keep schema migrations,
attachments and changes to the journal or locking mode in the App's normal
opener. The converter preserves WAL mode across reopen. Continue applying
connection-local settings in the App's normal opener, including any
non-default rollback journal mode.

The converter acts when the directory is NoCOW and the existing database is
CoW. It truncates the WAL with a fully synced checkpoint and checks for roughly
one database's logical size plus 1 MiB free on the volume. It rebuilds the
database with parameterized `VACUUM INTO` and restores WAL on the replacement
if the source used it. Then it checks the result with `quick_check(1)`, syncs
the replacement, and renames it over the original. The SDK then syncs the
parent directory. A failure before rename leaves the source
database in place. Retry after addressing the error. The next attempt discards
partial output from the previous attempt. If rename succeeded and directory
sync failed, the result is `changed=true` with an error. Allow enough time for
the copy and check to finish. The context can cancel the operation and the SDK
imposes no timeout.

Conversion requires a regular file with one hard link and matching ownership
and extended security metadata on the rebuilt file. It fails safely if SQLite
leaves sidecars after closing its handles. Resolve any persistent journal or WAL
configuration through SQLite before retrying. `ErrInsufficientSpace` identifies
a failed space check and can be handled by deferring the conversion. Keep the
existing database available to the App if your upgrade policy allows it.

`VACUUM INTO` rebuilds the database. Use explicit `INTEGER PRIMARY KEY` values
when applications depend on persistent row IDs. For details, see [SQLite
VACUUM].

## Schedule upkeep

| Helper | Default behavior |
| --- | --- |
| `Configure(ctx, conn)` | Set a 64 MiB retained journal size limit and a 1,000-page WAL auto-checkpoint threshold on the supplied `*sql.Conn`. |
| `Maintain(ctx, db)` | Run `PRAGMA optimize`. If the main DB uses incremental auto-vacuum and its freelist is at least 20% of all pages, reclaim up to 1,024 pages. |

Apply `Configure` on each physical writer connection outside a transaction.
For pooled drivers with initialization hooks or DSN pragmas, configure the
equivalent settings there so future connections receive them. The journal size
limit applies when SQLite resets or reuses the journal. An active WAL can grow
past the limit.

Call `Maintain` from an App-controlled schedule, such as once daily during a
quiet period. Serialize maintenance calls and log their errors. If a new DB
should support incremental vacuum, set `PRAGMA auto_vacuum=INCREMENTAL` before
creating its tables. For an existing DB, changing from auto-vacuum mode
`NONE` requires a separate explicit SQLite rebuild. See [SQLite pragmas].

[Btrfs file attributes]: https://btrfs.readthedocs.io/en/latest/ch-file-attributes.html
[SQLite WAL]: https://www.sqlite.org/wal.html
[SQLite VACUUM]: https://www.sqlite.org/lang_vacuum.html
[SQLite pragmas]: https://www.sqlite.org/pragma.html
