// Package-account lifecycle only writes a bounded control request and waits.
package lifecycle

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

type Config struct {
	Package, Data, Socket string
	// DeleteState is the admin's uninstall answer, written before a purge.
	DeleteState bool
	// Upgrade says a stop is for a package upgrade, written before the stop.
	Upgrade bool
	Log     func(string)
	Report  func(string)
}

var packageName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]{1,63}$`)

func absent(path string) (bool, error) {
	_, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return true, nil
	}
	return false, err
}
func Run(ctx context.Context, cfg Config, action string) error {
	if !packageName.MatchString(cfg.Package) || (action != "start" && action != "stop" && action != "purge") {
		return errors.New("invalid lifecycle request")
	}
	mailbox := filepath.Join(cfg.Data, "executor")
	if err := os.MkdirAll(mailbox, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(mailbox)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("unsafe control directory")
	}
	path := filepath.Join(mailbox, "control")
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return errors.New("unsafe control file")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	if action == "purge" {
		answer := "keep-state\n"
		if cfg.DeleteState {
			answer = "delete-state\n"
		}
		if err := writeAtomic(filepath.Join(mailbox, "uninstall"), answer); err != nil {
			return err
		}
	}
	if action == "stop" {
		reason := "stop\n"
		if cfg.Upgrade {
			reason = "upgrade\n"
		}
		if err := writeAtomic(filepath.Join(mailbox, "stop-reason"), reason); err != nil {
			return err
		}
	}
	witness := cfg.Socket
	if action == "purge" {
		witness = filepath.Dir(cfg.Socket)
	}
	initiallyGone, initialErr := absent(witness)
	observed := initialErr == nil && !initiallyGone
	if err := writeAtomic(path, action+"\n"); err != nil {
		return err
	}
	report := func(state string) {
		if cfg.Report != nil {
			cfg.Report(state)
		}
	}
	if action == "start" {
		report("accepted")
		return nil
	}
	// Offline requests still persist: the pinned boot task consumes pending purge.
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		gone, err := absent(cfg.Socket)
		if err == nil && gone && action == "purge" {
			// A paused executor has no socket but still polls the mailbox. Only
			// its purge removes the socket directory, after root cleanup.
			gone, err = absent(filepath.Dir(cfg.Socket))
		}
		if err != nil {
			if cfg.Log != nil {
				cfg.Log("executor socket status unavailable: cleanup pending; request retained; lifecycle continues")
			}
			report("cleanup-pending")
			return nil
		}
		if gone {
			if observed {
				report("cleanup-complete")
			} else {
				report("cleanup-pending")
			}
			return nil
		}
		observed = true
		select {
		case <-ctx.Done():
			if cfg.Log != nil {
				cfg.Log("executor " + action + " timeout: cleanup pending; request retained; uninstall may continue; purge mailbox is handled immediately, missing-package fallback requires 10 continuous minutes")
			}
			report("cleanup-pending")
			return nil
		case <-ticker.C:
		}
	}
}

func writeAtomic(path, content string) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.WriteString(content); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
