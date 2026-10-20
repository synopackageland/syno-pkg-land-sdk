// Linux/host fixture: a foreground process; never installed on a NAS.
package main

import (
	"encoding/json"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "cleanup":
			os.Exit(0)
		case "cleanup-slow":
			time.Sleep(2500 * time.Millisecond)
			os.Exit(0)
		case "cleanup-fail":
			os.Exit(2)
		case "cleanup-hang":
			signal.Ignore(syscall.SIGTERM, syscall.SIGINT)
			for {
				time.Sleep(time.Second)
			}
		}
	}
	if os.Getenv("APP_MODE") == "exit" {
		os.Exit(2)
	}
	raw, _ := json.Marshal(map[string]any{"uid": os.Geteuid(), "args": os.Args[1:], "env": os.Environ()})
	fmtOutput := append(raw, '\n')
	os.Stdout.Write(fmtOutput)
	if os.Getenv("APP_MODE") == "ignore" {
		signal.Ignore(syscall.SIGTERM, syscall.SIGINT)
		for {
			time.Sleep(time.Second)
		}
	}
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGTERM, syscall.SIGINT)
	<-c
}
