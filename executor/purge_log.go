package main

import (
	"log"
	"log/syslog"
)

// Purge removes executor.log with the protected state, so its diagnostics
// also go to the system log, where they survive the uninstall.
func purgeLogger() func(string) {
	system, err := syslog.New(syslog.LOG_NOTICE|syslog.LOG_DAEMON, "synopkgland-executor")
	return func(s string) {
		log.Print(s)
		if err == nil {
			_ = system.Notice(s)
		}
	}
}
