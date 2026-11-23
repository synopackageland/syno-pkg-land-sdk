package main

import (
	"runtime"
	"syscall"
)

func lowPriorityCompression() {
	// Linux nice values are per thread. Leave it locked until the worker
	// exits so other Go work never inherits the compressor's priority.
	runtime.LockOSThread()
	tid := syscall.Gettid()
	// getpriority's raw Linux value is 20 minus the nice value.
	if priority, err := syscall.Getpriority(syscall.PRIO_PROCESS, tid); err == nil && priority > 10 {
		_ = syscall.Setpriority(syscall.PRIO_PROCESS, tid, 10)
	}
}
