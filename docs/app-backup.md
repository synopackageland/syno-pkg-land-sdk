# Hyper Backup application backup

Native Apps built with synopkgland appear as applications in Hyper Backup's
backup wizard. An administrator can select the App, back up its data with the
rest of a backup task and restore it later. The CLI turns this on by default;
an App opts out with `backup: { hyper_backup: false }` in `dsm-package.yaml`
(see the CLI documentation for the field). Compose Apps keep their data in
container volumes, which this does not cover, so it is off for them.

## What is backed up

The App's data folder: the package's `var` folder, which DSM keeps across
upgrades (`/var/packages/<package>/var`). Everything in it is archived:
regular files, folders and symbolic links with their modes and modification
times. Sockets, pipes and devices are runtime state and are skipped. The
package's target folder (the program) is not backed up: it comes back with
the package itself. Shared Folders the App uses are ordinary Hyper Backup
folder sources and are not part of the application backup.

A restore replaces the whole data folder. The archive is first unpacked into a
private folder next to the data; only a complete and valid archive replaces
the current data, so a refused or broken archive leaves it unchanged.

## Offline and online

- **Offline (default).** Hyper Backup stops the App, the data folder is
  archived, and Hyper Backup starts the App again. Files are copied as they
  are, including SQLite journals, which is consistent because nothing writes
  while the App is stopped.
- **Online** (`backup: { online: true }`). The App keeps running. Each SQLite
  database (detected by its file header) is archived in place under a read
  transaction, without a copy of it (see [Online SQLite](#online-sqlite));
  its `-shm` and `-journal` files are left out. Other files are read as they
  are, so a file being written during the export may be inconsistent: list
  scratch folders in `exclude`, or use offline mode or a hook. A file or
  folder the App removes or renames after the export listed its folder is
  left out, a file it replaces is archived as the export opened it, and a
  file it shortens while it is read is padded with zeros to its earlier size.

A failed export or import also writes one line, tagged `synopkgland-backup`,
to the system log with the package, the path it stopped at and the error:
Hyper Backup discards the output of the backup scripts.

Hyper Backup always stops the App before a restore.

## Excluded paths

`backup: { exclude: [<pattern>, ...] }` leaves files and folders out of the
archive, in both modes. Each pattern is a `path.Match` glob relative to the
data folder, with `/` between folders: letters, digits, `.`, `_`, `-`, `*`
and `?`, at most 64 patterns of up to 255 characters, no `.` or `..` folder.
`*` does not cross `/`, and a matching folder is left out whole. For example
`[exports, ".restore-*", "cache/*.tmp"]`. Use it for scratch data the App
rebuilds or discards, especially SQLite files that are still being written
(for example by `VACUUM INTO`): online backup would otherwise wait for their
lock or archive them half written. A restore removes excluded paths with the
rest of the data folder.

## Online SQLite

For each database the export starts `sqlite3 <db>` as the package account,
opens a read transaction (`BEGIN; SELECT count(*) FROM sqlite_master;`), and
while it is open:

1. copies `<db>-wal` into a private folder in the data folder, and copies it
   again (at most three times) if its 32-byte header changed or the file got
   shorter during the copy;
2. streams the live database file into the archive;
3. adds the WAL copy as `<db>-wal`.

Closing sqlite3's input then ends the transaction. In WAL mode writers keep
committing the whole time; nothing waits on the export except a `FULL`,
`RESTART` or `TRUNCATE` checkpoint, which waits for readers within its busy
timeout and then reports busy. The extra disk write is the WAL, not the
database.

A database in a rollback-journal mode (`DELETE`, `TRUNCATE`, `PERSIST`; also
what `VACUUM INTO` writes) has no WAL to copy. Its read transaction holds a
SHARED lock, and a writer changes the database file only under an EXCLUSIVE
lock, so the file read is the last committed state; a hot journal left by a
crash is rolled back before the read. Its writers wait, within their own busy
timeout, until the file has been read, so keep databases written while the App
runs in WAL mode.

A restore opens the database without a `-shm`, so SQLite rebuilds the WAL
index by reading the WAL from its header and stops at the first frame whose
salt or checksum does not match; only whole committed transactions count.
Why a restored WAL-mode database is a committed state (`wal.c` of SQLite 3.46):

- **The WAL had frames not yet copied into the database when the transaction
  began.** The reader holds a read lock with a WAL mark. Restarting or
  truncating the WAL needs every such lock exclusively (`walRestartLog`,
  `walCheckpoint`), so the WAL only grows during the export. A checkpoint
  copies only frames up to the lowest reader mark into the database
  (`mxSafeFrame`), so every database page that changed, even half-written
  when it was read, has a frame in the copied WAL, and replaying the WAL
  replaces it. Frames appended later replay as later whole transactions; a
  partly written last frame fails its checksum.
- **The WAL was already fully copied into the database.** The reader holds
  read lock 0 and ignores the WAL. Any checkpoint copy needs read lock 0
  exclusively, so the database file does not change during the export. A
  writer may still restart the WAL from its start, or a `TRUNCATE` checkpoint
  empty it. A copy taken across such a change could pair the old header with
  part of the old frames and replay old page versions over the database, so
  the copy is checked: a restart writes a new header (new salts) before any
  frame, and a truncation shortens the file. A copy with an unchanged header
  and full length holds either the old WAL whole, whose frames the database
  already has, or new frames written on top of the unchanged database.
- A checkpoint can shorten the database file only to the size of the latest
  commit, and only when no reader's mark is behind it; bytes missing past the
  read size are archived as zeros, beyond the snapshot's last page.

## Quiesce hook

`backup: { hook: <path> }` names an executable in the package, relative to
the package target. It runs as the package account as `<hook> freeze` before
the export and `<hook> thaw` after it, also when the export fails. A failing
`freeze` stops the export. The hook must not write to standard output; its
output goes to standard error. Each call may take up to 10 minutes.

## Data version

`backup: { data_version: "major.minor" }` (default `"1.0"`) is recorded in
the archive. A restore accepts an archive from the same package with the same
major version and an equal or lower minor version. Raise the minor version
when newer data stays readable by the App's own migration, and the major
version when older data can no longer be restored. A refused restore leaves
the current data unchanged and the restore reports an error.

## Who runs what

Hyper Backup runs a package's backup scripts as **root**. This is how DSM
starts Hyper Backup's application plugins; it is a DSM-owned hook, not part of
the App's runtime. The App itself still runs as its package account, and the
SDK privilege tiers for App code are unchanged: an App does not gain root by
being backed up, and it cannot add code to the root part.

The root part is a short, fixed script that the CLI generates and that
`synopkgland inspect` verifies byte for byte. It:

1. reads Hyper Backup's temporary folder for this run;
2. takes the owner of the App's data folder (the package account) and refuses
   to continue if that owner is root;
3. switches to that account, which then runs the SDK backup control shipped in
   the package;
4. passes one archive, unread, between that account and the temporary folder:
   the account writes it for a backup and reads it for a restore.

Root never parses or unpacks the archive and never runs package files. If a
future Hyper Backup starts the scripts as another account, the script skips
the switch. The package ships no script that Hyper Backup would run while it
only lists applications, and inspection refuses an SPK that does.

The archive and its contents are handled by the `appbackup` Go package
(`appbackup/cmd/control`), which refuses to run as root.

## Limits

- The package account must be able to read every file in the data folder.
  A file it cannot read fails the backup instead of being skipped.
- Restoring needs the App installed. Install it from its SPK first, then
  restore the application from Hyper Backup.
- Hyper Backup application backup was verified on DSM 7.3 with Hyper Backup
  4.1. Other DSM 7 versions are expected to behave the same but are not yet
  verified on a device.
