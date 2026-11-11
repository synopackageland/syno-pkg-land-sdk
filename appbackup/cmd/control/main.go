// Command control is the package-account half of Hyper Backup application
// backup. The generated root scripts start it as the package account with
// `export` (archive on stdout) or `import` (archive on stdin).
package main

import (
	"fmt"
	"log/syslog"
	"os"
	"path/filepath"
	"syscall"

	"github.com/synopackageland/syno-pkg-land-sdk/appbackup"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		// Hyper Backup discards stderr; the system log keeps the reason.
		if l, e := syslog.New(syslog.LOG_ERR|syslog.LOG_USER, "synopkgland-backup"); e == nil {
			l.Err(err.Error())
			l.Close()
		}
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) != 2 || os.Getuid() == 0 || os.Getuid() != os.Geteuid() {
		return fmt.Errorf("%w: run as the package account with export or import", appbackup.ErrInvalid)
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	// The binary ships as <target>/backup/control next to config.json.
	dir := filepath.Dir(exe)
	c, err := appbackup.LoadConfig(filepath.Join(dir, "config.json"))
	if err != nil {
		return err
	}
	data := filepath.Join("/var/packages", c.Package, "var")
	var st syscall.Stat_t
	if err := syscall.Stat(data, &st); err != nil || int(st.Uid) != os.Getuid() {
		return fmt.Errorf("%w: the data directory must belong to the package account", appbackup.ErrInvalid)
	}
	o := appbackup.Options{Target: filepath.Dir(dir)}
	switch os.Args[1] {
	case "export":
		err = appbackup.Export(data, os.Stdout, c, o)
	case "import":
		err = appbackup.Import(data, os.Stdin, c, o)
	default:
		return fmt.Errorf("%w: unsupported action", appbackup.ErrInvalid)
	}
	if err != nil {
		return fmt.Errorf("%s %s: %w", c.Package, os.Args[1], err)
	}
	return nil
}
