// Command describe is the build-only bridge registry exporter. It never runs an App.
package main

import (
	"encoding/json"
	"github.com/synopackageland/syno-pkg-land-sdk/bridge"
	"log"
	"os"
)

func main() {
	if len(os.Args) != 4 {
		log.Fatal("usage: describe <bridge.json> <permissions.json> <portal-executor:true|false>")
	}
	raw, err := os.ReadFile(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	permissions, err := os.ReadFile(os.Args[2])
	if err != nil {
		log.Fatal(err)
	}
	routes, err := bridge.DescribeRuntimeRoutes(raw, permissions, os.Args[3] == "true")
	if err != nil {
		log.Fatal(err)
	}
	if err = json.NewEncoder(os.Stdout).Encode(routes); err != nil {
		log.Fatal(err)
	}
}
