//go:build linux

package supervision

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

// WatchNetwork reports kernel route/link/address changes and resolver writes.
// It uses blocking event descriptors, not a polling/retry timer. The caller
// decides whether a failed, enabled child should begin a new bounded series.
func WatchNetwork(ctx context.Context, changed func()) error {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, unix.NETLINK_ROUTE)
	if err != nil {
		return err
	}
	if err = unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK, Groups: unix.RTMGRP_LINK | unix.RTMGRP_IPV4_IFADDR | unix.RTMGRP_IPV6_IFADDR | unix.RTMGRP_IPV4_ROUTE | unix.RTMGRP_IPV6_ROUTE}); err != nil {
		unix.Close(fd)
		return err
	}
	routeFile := os.NewFile(uintptr(fd), "route-events")
	var wg sync.WaitGroup
	notify := make(chan struct{}, 1)
	send := func() {
		select {
		case notify <- struct{}{}:
		default:
		}
	}
	// Shutdown wakes a blocked recv without a periodic timeout.
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, 8192)
		for {
			n, err := routeFile.Read(buf)
			if err != nil {
				if errors.Is(err, unix.EINTR) {
					continue
				}
				return
			}
			if n > 0 {
				send()
			}
		}
	}()
	var dnsFile *os.File
	dns, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err == nil {
		dnsFile = os.NewFile(uintptr(dns), "resolver-events")
		_, _ = unix.InotifyAddWatch(dns, "/etc", unix.IN_CLOSE_WRITE|unix.IN_MOVED_TO|unix.IN_CREATE)
		_, _ = unix.InotifyAddWatch(dns, "/etc/resolv.conf", unix.IN_CLOSE_WRITE|unix.IN_MODIFY|unix.IN_DELETE_SELF|unix.IN_MOVE_SELF)
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := make([]byte, 4096)
			for {
				n, err := dnsFile.Read(buf)
				if err != nil {
					if errors.Is(err, unix.EINTR) {
						continue
					}
					return
				}
				for i := 0; i+16 <= n; {
					size := int(binary.NativeEndian.Uint32(buf[i+12 : i+16]))
					if i+16+size > n {
						break
					}
					name := strings.TrimRight(string(buf[i+16:i+16+size]), "\x00")
					if name == "" || name == "resolv.conf" {
						send()
					}
					i += 16 + size
				}
			}
		}()
	}
	defer func() {
		routeFile.Close()
		if dnsFile != nil {
			dnsFile.Close()
		}
		wg.Wait()
	}()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-notify:
			if ctx.Err() == nil {
				changed()
			}
		}
	}
}
