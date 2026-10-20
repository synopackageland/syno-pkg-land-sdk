// Canonical renderer used to check CLI/executor byte equivalence locally.
package main

import (
	"encoding/json"
	"github.com/synopackageland/syno-pkg-land-sdk/executor/internal/taskscript"
	"os"
)

func main() {
	if len(os.Args) != 3 {
		os.Exit(2)
	}
	tasks, err := taskscript.Generate(os.Args[1], os.Args[2])
	if err != nil {
		os.Exit(1)
	}
	json.NewEncoder(os.Stdout).Encode(tasks)
}
