// A real executor process: its verified peer outlives the callback and closes
// only when the process exits. The child is stopped/reaped on its owning thread.
package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"runtime"

	"github.com/synopackageland/syno-pkg-land-sdk/ipc"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	const path = "/var/run/synopkgland/PeerShutdownFixture/helper.sock"
	handled := make(chan error, 1)
	var server *ipc.PeerServer
	var err error
	server, err = ipc.StartPeerUnix(context.Background(), path, ipc.PeerPolicy{UID: 1000, GID: 1000},
		func(_ context.Context, conn net.Conn, peer ipc.PeerCredentials) {
			// Returning does not close conn: the process owns it until exit.
			handled <- func() error {
				runtime.LockOSThread()
				defer runtime.UnlockOSThread()
				child := exec.Command("sleep", "60")
				if err := child.Start(); err != nil {
					return err
				}
				request := make([]byte, 5)
				_, readErr := io.ReadFull(conn, request)
				killErr := child.Process.Kill()
				waitErr := child.Wait()
				if readErr != nil {
					return readErr
				}
				if string(request) != "stop\n" || killErr != nil || waitErr == nil || child.ProcessState == nil {
					return fmt.Errorf("child stop/reap failed: %q %v %v", request, killErr, waitErr)
				}
				if peer.UID != 1000 {
					return fmt.Errorf("wrong peer: %+v", peer)
				}
				if err := server.StopAccepting(); err != nil {
					return err
				}
				_, err := io.WriteString(conn, "{\"ok\":true,\"child_reaped\":true}\n")
				return err
			}()
		})
	if err != nil {
		return err
	}
	// Deliberately do not Close: the last peer must live until process exit.
	fmt.Println("ready")
	if err := <-handled; err != nil {
		return err
	}
	fmt.Println("retained")
	var exit [1]byte
	_, err = io.ReadFull(os.Stdin, exit[:])
	return err
}
