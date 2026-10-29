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
  database (detected by its file header) is copied through SQLite's backup API
  (`sqlite3 .backup`) into a consistent snapshot, and its `-wal`, `-shm` and
  `-journal` files are left out. Other files are copied as they are, so an
  App that writes other files during a backup should use offline mode or a
  hook.

Hyper Backup always stops the App before a restore.

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
