package idgateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"
)

// Deletions acts on DSM account deletions without root. Every uid that signs
// in is recorded; a local account that then leaves the passwd file is deleted
// at once: its gateway sessions are revoked and Actions run. A domain account
// is never deleted automatically, since a directory outage looks the same:
// Status lists the ones NSS can no longer find and an administrator Purges
// them. A failed deletion stays pending in the state directory and is retried
// on every Reconcile.
type Deletions struct {
	StateDir string
	// Passwd is the local account file, /etc/passwd.
	Passwd string
	// Names resolves uids through NSS, for domain accounts.
	Names   Names
	Actions []DeleteAction
	// VarDir is the package var directory remove actions are relative to.
	VarDir string
	// Revoke, when set, ends the gateway's sessions of a deleted uid.
	Revoke func(uid int)
	// Deleted, when set, is told of each deletion whose actions all succeeded.
	Deleted func(PendingDeletion)
	Now     func() time.Time
}

// PendingDeletion is a deletion whose actions have not all succeeded yet.
type PendingDeletion struct {
	UID      int       `json:"uid"`
	Name     string    `json:"name"`
	Since    time.Time `json:"since"`
	Attempts int       `json:"attempts"`
	Error    string    `json:"error,omitempty"`
}

// SeenUID is an account that signed in.
type SeenUID struct {
	UID  int    `json:"uid"`
	Name string `json:"name"`
}

// AccountStatus is what an administrator needs to see: deletions still
// pending, with their last error, and domain accounts whose DSM user can no
// longer be found.
type AccountStatus struct {
	Pending []PendingDeletion `json:"pending"`
	Missing []SeenUID         `json:"missing"`
}

func (d *Deletions) seen() seenStore {
	return seenStore{path: filepath.Join(d.StateDir, "seen-uids.json")}
}

func (d *Deletions) pendingPath(uid int) string {
	return filepath.Join(d.StateDir, "pending", strconv.Itoa(uid)+".json")
}

// SignedIn records a verified sign-in.
func (d *Deletions) SignedIn(s Subject) error {
	local, _ := localAccounts(d.Passwd)
	return d.seen().record(s, local)
}

// Reconcile compares the recorded local accounts with the passwd file, queues
// a deletion for each one that is gone, and retries every pending deletion.
// An unreadable or incomplete passwd file deletes nothing.
func (d *Deletions) Reconcile(ctx context.Context) error {
	unlock, err := lockFile(filepath.Join(d.StateDir, "account.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	seen, err := d.seen().load()
	if err != nil {
		return err
	}
	local, diffErr := localAccounts(d.Passwd)
	if diffErr == nil {
		for uid, a := range seen {
			if _, ok := local[uid]; a.Local && !ok {
				if err := d.queue(uid, a.Name); err != nil {
					return err
				}
			}
		}
	}
	return errors.Join(diffErr, d.retry(ctx, nil))
}

// Purge deletes a recorded account that NSS can no longer find, for domain
// accounts, which are never deleted automatically.
func (d *Deletions) Purge(ctx context.Context, uid int) error {
	unlock, err := lockFile(filepath.Join(d.StateDir, "account.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	seen, err := d.seen().load()
	if err != nil {
		return err
	}
	a, ok := seen[uid]
	if !ok {
		return fmt.Errorf("uid %d never signed in through the gateway", uid)
	}
	if _, err := d.Names.Name(ctx, uid); !errors.Is(err, ErrNoAccount) {
		return fmt.Errorf("uid %d is still a DSM account (%v); only accounts DSM no longer has can be purged", uid, err)
	}
	if err := d.queue(uid, a.Name); err != nil {
		return err
	}
	return d.retry(ctx, []int{uid})
}

// queue records a pending deletion unless one exists.
func (d *Deletions) queue(uid int, name string) error {
	if _, err := os.Stat(d.pendingPath(uid)); err == nil {
		return nil
	}
	return d.savePending(PendingDeletion{UID: uid, Name: name, Since: d.Now().UTC()})
}

func (d *Deletions) savePending(p PendingDeletion) error {
	raw, _ := json.Marshal(p)
	return replacePrivate(d.pendingPath(p.UID), raw)
}

func (d *Deletions) loadPending() ([]PendingDeletion, error) {
	entries, err := os.ReadDir(filepath.Join(d.StateDir, "pending"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []PendingDeletion
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" || e.Name()[0] == '.' {
			continue
		}
		raw, err := readPrivate(filepath.Join(d.StateDir, "pending", e.Name()))
		if err != nil {
			return nil, err
		}
		var p PendingDeletion
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("pending %s: %w", e.Name(), err)
		}
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b PendingDeletion) int { return a.UID - b.UID })
	return out, nil
}

// retry runs the pending deletions, only those of uids when given; callers
// hold the account lock.
func (d *Deletions) retry(ctx context.Context, uids []int) error {
	pending, err := d.loadPending()
	if err != nil {
		return err
	}
	runner := actionRunner{varDir: d.VarDir}
	var errs []error
	for _, p := range pending {
		if uids != nil && !slices.Contains(uids, p.UID) {
			continue
		}
		if d.Revoke != nil {
			d.Revoke(p.UID)
		}
		if err := runner.run(ctx, d.Actions, Subject{UID: p.UID, Name: p.Name}); err != nil {
			p.Attempts, p.Error = p.Attempts+1, err.Error()
			errs = append(errs, fmt.Errorf("delete uid %d: %w", p.UID, err), d.savePending(p))
			continue
		}
		if err := d.seen().forget(p.UID); err != nil {
			errs = append(errs, err)
			continue
		}
		errs = append(errs, os.Remove(d.pendingPath(p.UID)))
		if d.Deleted != nil {
			d.Deleted(p)
		}
	}
	return errors.Join(errs...)
}

// Status lists pending deletions and recorded domain accounts NSS cannot find.
func (d *Deletions) Status(ctx context.Context) (AccountStatus, error) {
	st := AccountStatus{Pending: []PendingDeletion{}, Missing: []SeenUID{}}
	seen, err := d.seen().load()
	if err != nil {
		return st, err
	}
	pending, err := d.loadPending()
	if err != nil {
		return st, err
	}
	st.Pending = append(st.Pending, pending...)
	for uid, a := range seen {
		if a.Local || slices.ContainsFunc(pending, func(p PendingDeletion) bool { return p.UID == uid }) {
			continue
		}
		if _, err := d.Names.Name(ctx, uid); errors.Is(err, ErrNoAccount) {
			st.Missing = append(st.Missing, SeenUID{UID: uid, Name: a.Name})
		}
	}
	slices.SortFunc(st.Missing, func(a, b SeenUID) int { return a.UID - b.UID })
	return st, nil
}

// Retry delays of pending deletions: the first one, doubled after each
// failure up to maxPendingRetry.
var (
	pendingRetry    = 5 * time.Second
	maxPendingRetry = 5 * time.Minute
)

// Watch reconciles once, which catches deletions made while the package was
// stopped, and again after each change of the passwd file, until ctx ends.
// While deletions are pending, as when the app is not up yet, it also retries
// them with a growing delay.
func (d *Deletions) Watch(ctx context.Context, logf func(format string, args ...any)) error {
	ctx, cancel := context.WithCancel(ctx)
	pending := make(chan struct{}, 1)
	reconcile := func() {
		if err := d.Reconcile(ctx); err != nil {
			logf("account deletions: %v", err)
		}
		if p, _ := d.loadPending(); len(p) > 0 {
			select {
			case pending <- struct{}{}:
			default:
			}
		}
	}
	retried := make(chan struct{})
	go func() { retryPending(ctx, pending, reconcile); close(retried) }()
	defer func() { cancel(); <-retried }()
	reconcile()
	return watchFile(ctx, filepath.Dir(d.Passwd), filepath.Base(d.Passwd), reconcile)
}

// retryPending calls reconcile a while after each signal on pending; the
// delay grows while reconcile keeps signalling and resets once it does not.
func retryPending(ctx context.Context, pending chan struct{}, reconcile func()) {
	delay := pendingRetry
	for {
		select {
		case <-ctx.Done():
			return
		case <-pending:
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		reconcile()
		if len(pending) > 0 {
			delay = min(2*delay, maxPendingRetry)
		} else {
			delay = pendingRetry
		}
	}
}
