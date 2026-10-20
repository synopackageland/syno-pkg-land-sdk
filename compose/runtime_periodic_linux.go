package compose

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// RunWithPeriodic supervises the main process and nonoverlapping interval
// commands as the current nonroot account. Cancel terminates their process
// groups, waits at most five seconds for the main, and reaps its direct children.
func RunWithPeriodic(ctx context.Context, argv []string, tasks []PeriodicCommand) error {
	if os.Geteuid() == 0 || os.Getuid() != os.Geteuid() || len(argv) == 0 {
		return fmt.Errorf("[COMPOSE_RUNTIME_COMMAND_INVALID] nonroot main argv required")
	}
	// Reuse the public declaration validator for exported callers too.
	if err := validateRuntime(StorageInitialization{Runtime: &RuntimeConfiguration{Periodic: tasks}}); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	main := exec.Command(argv[0], argv[1:]...)
	main.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	main.Stdout, main.Stderr = os.Stdout, os.Stderr
	if err := main.Start(); err != nil {
		return fmt.Errorf("[COMPOSE_RUNTIME_COMMAND_INVALID] main could not start")
	}
	done := make(chan error, 1)
	go func() { done <- main.Wait() }()
	var wg sync.WaitGroup
	for index, task := range tasks {
		wg.Go(func() {
			timer := time.NewTimer(time.Duration(task.IntervalSeconds) * time.Second)
			defer timer.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-timer.C:
					jobCtx, stop := context.WithTimeout(ctx, time.Duration(task.TimeoutSeconds)*time.Second)
					err := runPrepare(jobCtx, nil, PrepareCommand{Argv: task.Argv})
					stop()
					if err != nil && ctx.Err() == nil {
						fmt.Fprintf(os.Stderr, "[COMPOSE_PERIODIC_FAILED] task %d failed\n", index)
					}
					timer.Reset(time.Duration(task.IntervalSeconds) * time.Second)
				}
			}
		})
	}
	var err error
	select {
	case err = <-done:
		// The main may leave child processes behind even on a normal exit.
		syscall.Kill(-main.Process.Pid, syscall.SIGTERM)
	case <-ctx.Done():
		syscall.Kill(-main.Process.Pid, syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			syscall.Kill(-main.Process.Pid, syscall.SIGKILL)
			<-done
		}
	}
	cancel()
	wg.Wait()
	return err
}
