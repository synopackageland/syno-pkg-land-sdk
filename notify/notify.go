// Package notify sends App-owned notifications as the package account.
// Apps retain their durable outbox, destination revisions and retry scheduling.
package notify

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"time"
)

// NewDeliveryID creates an identity to persist before the first network attempt.
func NewDeliveryID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(id[:]), nil
}

var deliveryID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,199}$`)
var eventID = regexp.MustCompile(`^[a-z][a-z0-9_.]*$`)

type Message struct {
	DeliveryID          string          `json:"delivery_id"`
	DestinationRevision string          `json:"destination_revision"`
	Channel             string          `json:"channel"`
	Payload             json.RawMessage `json:"payload"`
}

type Receipt struct {
	DeliveryID        string            `json:"delivery_id"`
	State             string            `json:"state"` // failed or handed-off, never proof of human receipt
	PossiblyDuplicate bool              `json:"possibly_duplicate"`
	Recipients        map[string]string `json:"recipients,omitempty"`
}

// DeliveryError contains a safe code only, never a URL, credential or SMTP reply.
type DeliveryError struct {
	Code       string
	Retryable  bool
	RetryAfter time.Duration
}

func (e *DeliveryError) Error() string { return e.Code }

type Secrets interface {
	Resolve(context.Context, string) (string, error)
}

type Event struct {
	Vars     []string
	Throttle time.Duration // zero selects ten minutes; negative values are invalid
}

type Options struct {
	Key        string
	Users      []string // nil selects @administrators
	DeliveryID string   // retries reuse the identity persisted by the App
}

// Record is an immutable admission to the App's event log/outbox.
type Record struct {
	DeliveryID string            `json:"delivery_id"`
	Event      string            `json:"event"`
	Key        string            `json:"key"`
	Vars       map[string]string `json:"vars"`
	Users      []string          `json:"users"`
}

// Recorder durably admits the event before any send. A transactional App writes
// the event and its outbox together in Admit. Implementations must
// bind an ID to immutable content, refuse changed content under that ID, return
// the original ID for a repeated event+key within interval, and let retries of
// an existing ID resume outside throttling. The bool is true only for a new
// admission. Implement it in the App's transaction, shared by all CGI/workers.
// It must not perform network I/O.
type Recorder interface {
	Admit(context.Context, Record, time.Duration) (Record, bool, error)
}

type Result struct {
	DeliveryID string `json:"delivery_id"`
	Throttled  bool   `json:"throttled"`
}

// Notifier validates event variables and durably admits work. The App's worker
// sends each pinned destination with Sender.Notify and persists each Receipt.
type Notifier struct {
	Events   map[string]Event
	Recorder Recorder
}

func (n Notifier) Notify(ctx context.Context, event string, vars map[string]string, opts Options) (Result, error) {
	def, ok := n.Events[event]
	if !ok || !eventID.MatchString(event) || def.Throttle < 0 || len(opts.Key) > 1024 {
		return Result{}, errors.New("invalid notification event")
	}
	if n.Recorder == nil {
		return Result{}, errors.New("notification recorder is required")
	}
	expected := make(map[string]bool, len(def.Vars))
	for _, name := range def.Vars {
		if name == "" || expected[name] {
			return Result{}, errors.New("invalid notification variables")
		}
		expected[name] = true
	}
	if len(vars) != len(expected) {
		return Result{}, errors.New("invalid notification variables")
	}
	values := make(map[string]string, len(vars))
	for key, value := range vars {
		if !expected[key] {
			return Result{}, errors.New("invalid notification variables")
		}
		values[key] = value
	}
	id := opts.DeliveryID
	if id == "" {
		var err error
		id, err = NewDeliveryID()
		if err != nil {
			return Result{}, err
		}
	}
	if !deliveryID.MatchString(id) {
		return Result{}, errors.New("invalid delivery identity")
	}
	users := append([]string(nil), opts.Users...)
	if opts.Users == nil {
		users = []string{"@administrators"}
	}
	if len(users) == 0 || len(users) > 100 {
		return Result{}, errors.New("invalid notification users")
	}
	for _, user := range users {
		if user == "" || len(user) > 256 {
			return Result{}, errors.New("invalid notification users")
		}
	}
	sort.Strings(users)
	interval := def.Throttle
	if interval == 0 {
		interval = 10 * time.Minute
	}
	record, created, err := n.Recorder.Admit(ctx, Record{id, event, opts.Key, values, users}, interval)
	if err != nil {
		return Result{DeliveryID: id}, err
	}
	if !deliveryID.MatchString(record.DeliveryID) {
		return Result{}, errors.New("invalid recorded delivery identity")
	}
	return Result{DeliveryID: record.DeliveryID, Throttled: !created && record.DeliveryID != id}, nil
}
