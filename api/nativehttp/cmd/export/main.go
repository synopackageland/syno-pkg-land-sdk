// Command export emits metadata only; it never initializes an App or gateway.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/synopackageland/syno-pkg-land-sdk/api/nativehttp"
)

func main() {
	registry := flag.String("registry", "", "v3 registry JSON file")
	config := flag.String("config", "", "resolved BuildConfig JSON file")
	flag.Parse()
	if *registry == "" || *config == "" || flag.NArg() != 0 {
		flag.Usage()
		os.Exit(2)
	}
	raw, e := os.ReadFile(*registry)
	if e != nil {
		fail(e)
	}
	file, e := os.Open(*config)
	if e != nil {
		fail(e)
	}
	var c nativehttp.BuildConfig
	d := json.NewDecoder(file)
	d.DisallowUnknownFields()
	e = d.Decode(&c)
	file.Close()
	if e != nil {
		fail(e)
	}
	a, e := nativehttp.Export(raw, c)
	if e != nil {
		fail(e)
	}
	if e = json.NewEncoder(os.Stdout).Encode(a); e != nil {
		fail(e)
	}
}
func fail(e error) { fmt.Fprintln(os.Stderr, e); os.Exit(1) }
