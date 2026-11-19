// Foreground listener for the installer/supervisor regression; never packaged.
package main

import (
	"io"
	"net"
	"os"
)

func main() {
	port := os.Getenv("SYNOPKGLAND_PORT_GATEWAY_ORIGIN")
	listener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", port))
	if err != nil {
		panic(err)
	}
	defer listener.Close()
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		io.WriteString(conn, port+"\n")
		conn.Close()
	}
}
