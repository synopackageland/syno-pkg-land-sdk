package main

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

func samePackagePins(ctx context.Context, dir, executorDigest, policyDigest string) bool {
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return false
	}
	ed, err := digestFile(ctx, dir+"/executor", maxExecutorBytes)
	if err != nil || ed != executorDigest {
		return false
	}
	pd, err := digestFile(ctx, dir+"/policy.json", maxPolicyBytes)
	return err == nil && pd == policyDigest
}

// The process and monitor outlive its listener. A package start can only expose
// the already-approved runtime when the shipped bytes still match both pins;
// samePins reports why it refused.
func manageService(ctx context.Context, actions <-chan string, open func() (func(), error), samePins func(context.Context) bool, purge func() error) error {
	var closeService func()
	var checking <-chan bool
	var cancelCheck context.CancelFunc
	pause := func() {
		if cancelCheck != nil {
			cancelCheck()
			// An anchored install in the check removes its staged copy on return.
			<-checking
			cancelCheck = nil
			checking = nil
		}
		if closeService != nil {
			closeService()
			closeService = nil
		}
	}
	defer pause()
	resume := func() {
		if closeService != nil || checking != nil {
			return
		}
		checkCtx, cancel := context.WithCancel(ctx)
		cancelCheck = cancel
		result := make(chan bool, 1)
		checking = result
		go func() { result <- samePins(checkCtx) }()
	}
	resume()
	for {
		select {
		case <-ctx.Done():
			return nil
		case matches := <-checking:
			cancelCheck()
			cancelCheck = nil
			checking = nil
			if ctx.Err() != nil {
				return nil
			}
			if !matches {
				continue
			}
			var err error
			closeService, err = open()
			if err != nil {
				return err
			}
		case action, ok := <-actions:
			if !ok {
				return nil
			}
			switch action {
			case "stop":
				pause()
			case "start":
				resume()
			case "purge":
				pause()
				return purge()
			}
		}
	}
}

// Root task control never writes through the package-owned mailbox. Only a
// process whose protected pid/executable was verified can receive these signals.
func rootControlSignals(parent context.Context, actions chan<- string) func() {
	ctx, cancel := context.WithCancel(parent)
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, syscall.SIGUSR1, syscall.SIGUSR2)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case sig := <-signals:
				action := "stop"
				if sig == syscall.SIGUSR2 {
					action = "purge"
				}
				select {
				case <-ctx.Done():
					return
				case actions <- action:
				}
			}
		}
	}()
	return func() { signal.Stop(signals); cancel(); <-done }
}
