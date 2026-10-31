package supervision

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestBackoffJitterStableResetAndIntensity(t *testing.T) {
	now := time.Unix(1000, 0)
	cfg := Defaults()
	p := New(cfg, func(n time.Duration) time.Duration { return n - 1 })
	for i := 0; i < cfg.Burst; i++ {
		p.Started(now)
		s := p.Exited(now.Add(time.Millisecond), Exit{Code: 1, Reason: "child_exit"})
		if i == cfg.Burst-1 {
			if s.State != Failed || !s.NextRetry.IsZero() {
				t.Fatalf("terminal: %+v", s)
			}
		} else {
			cap := min(cfg.Initial<<i, cfg.Max)
			delay := s.NextRetry.Sub(now.Add(time.Millisecond))
			if s.State != Backoff || delay < cfg.Min || delay >= cap+cfg.Min {
				t.Fatalf("jitter %d: %+v", i, s)
			}
		}
		now = now.Add(time.Second)
	}
	// A window expiring alone is not a trigger.
	if s := p.Exited(now.Add(24*time.Hour), Exit{Code: 1}); s.State != Failed || !s.NextRetry.IsZero() {
		t.Fatal(s)
	}
	p.Trigger()
	p.Started(now)
	if s := p.Exited(now.Add(time.Second), Exit{Code: 1}); s.State != Backoff || s.Attempts != 1 {
		t.Fatal(s)
	}
	p.Started(now)
	if s := p.Exited(now.Add(61*time.Second), Exit{Code: 1}); s.Attempts != 1 {
		t.Fatal("stable run did not reset", s)
	}
}

func TestPermanentExits(t *testing.T) {
	for _, exit := range []Exit{{Code: 78, Permanent: true, Reason: "invalid_config"}, {Code: 0, Permanent: true}} {
		p := New(Defaults(), nil)
		p.Started(time.Now())
		s := p.Exited(time.Now(), exit)
		if !s.NextRetry.IsZero() || s.State == Backoff {
			t.Fatal(s)
		}
		if exit.Permanent && s.State != Failed {
			t.Fatal(s)
		}
	}
	if PermanentStart(errors.New("unknown")) {
		t.Fatal("unknown start errors must remain retryable")
	}
}

func TestFullJitterBounds(t *testing.T) {
	for _, sample := range []func(time.Duration) time.Duration{
		func(time.Duration) time.Duration { return 0 },
		func(n time.Duration) time.Duration { return n - 1 },
	} {
		cfg := Defaults()
		cfg.Burst = 100
		cfg.Window = time.Hour
		p := New(cfg, sample)
		now := time.Now()
		for i := 0; i < 20; i++ {
			p.Started(now)
			s := p.Exited(now, Exit{Code: 1})
			d := s.NextRetry.Sub(now)
			if d < cfg.Min || d > cfg.Max {
				t.Fatal(d)
			}
		}
	}
}

func TestStableWindowScalesWithBackoffAndKeepsShortRuns(t *testing.T) {
	cfg := Defaults()
	cfg.Burst = 100
	cfg.Window = time.Hour
	p := New(cfg, func(n time.Duration) time.Duration { return n - 1 })
	now := time.Unix(100, 0)
	for i := 0; i < 7; i++ {
		p.Started(now)
		p.Exited(now.Add(time.Second), Exit{Code: 1})
		now = now.Add(time.Second)
	}
	p.Started(now)
	if s := p.Exited(now.Add(61*time.Second), Exit{Code: 1}); s.Attempts != 8 {
		t.Fatal("one-minute run reset a larger backoff", s)
	}
	p.Started(now)
	if s := p.Exited(now.Add(601*time.Second), Exit{Code: 1}); s.Attempts != 1 {
		t.Fatal("long stable run did not reset", s)
	}
}

func TestResidentUnexpectedExitsShareTheRestartBudget(t *testing.T) {
	for _, code := range []int{0, 1, -1} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			cfg := Defaults()
			p := New(cfg, func(n time.Duration) time.Duration { return n - 1 })
			now := time.Unix(1000, 0)
			for attempt := 1; attempt <= cfg.Burst; attempt++ {
				p.Started(now)
				p.Running()
				s := p.Exited(now, Exit{Code: code, Reason: "child_exit"})
				if s.Attempts != attempt || s.LastExit.Code != code {
					t.Fatal(s)
				}
				if attempt == cfg.Burst {
					if s.State != Failed || !s.NextRetry.IsZero() {
						t.Fatal(s)
					}
				} else {
					if s.State != Backoff || s.NextRetry.Sub(now) != min(cfg.Initial<<(attempt-1), cfg.Max) {
						t.Fatal(s)
					}
					now = s.NextRetry
				}
			}
			p.Trigger()
			p.Started(now)
			if s := p.Exited(now, Exit{Code: code}); s.State != Backoff || s.Attempts != 1 {
				t.Fatal(s)
			}
		})
	}
}

func TestTaskCleanExitAndRequestedStop(t *testing.T) {
	for _, mode := range []Mode{OnFailure, Off} {
		cfg := Defaults()
		cfg.Mode = mode
		p := New(cfg, nil)
		p.Started(time.Now())
		if s := p.Exited(time.Now(), Exit{Code: 0}); s.State != Stopped || !s.NextRetry.IsZero() || s.Attempts != 0 {
			t.Fatal(s)
		}
	}
	for _, code := range []int{0, 1, -1} {
		p := New(Defaults(), nil)
		p.Started(time.Now())
		p.Exited(time.Now(), Exit{Code: 1})
		if s := p.Stop(); s.State != Stopped || !s.NextRetry.IsZero() {
			t.Fatal(s)
		}
		p.Started(time.Now())
		p.Running()
		p.Waiting("dependency")
		if s := p.Exited(time.Now(), Exit{Code: code}); s.State != Stopped || !s.NextRetry.IsZero() {
			t.Fatal(s)
		}
		p.Trigger()
		p.Started(time.Now())
		if s := p.Exited(time.Now(), Exit{Code: 0}); s.State != Backoff {
			t.Fatal(s)
		}
	}
}
