package main

import (
	"flag"
	"fmt"
	"github.com/synopackageland/syno-pkg-land-sdk/installsettings"
	"os"
	"path/filepath"
)

func main() {
	if len(os.Args) < 2 || os.Getuid() == 0 || os.Getuid() != os.Geteuid() {
		fmt.Fprintln(os.Stderr, "INSTALL_SETTINGS_INVALID: package account required")
		os.Exit(1)
	}
	fs := flag.NewFlagSet("install-settings", flag.ExitOnError)
	root := fs.String("root", "", "package payload")
	pkg := fs.String("package", "", "package ID")
	fs.Parse(os.Args[2:])
	var err error
	_, portsError := os.Stat(filepath.Join(*root, "install/ports.json"))
	_, settingsError := os.Stat(filepath.Join(*root, "install/settings.json"))
	if portsError == nil {
		switch os.Args[1] {
		case "prepare":
			err = installsettings.PreparePorts(*root, *pkg, "")
		case "remember":
			err = installsettings.RememberPorts(*root, *pkg, "")
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	if settingsError != nil && os.Args[1] != "purge" {
		return
	}
	switch os.Args[1] {
	case "prepare":
		err = installsettings.Prepare(*root, *pkg, "")
	case "remember":
		err = installsettings.Remember(*root, *pkg, "")
	case "purge":
		err = installsettings.Purge(*pkg, "")
	default:
		err = fmt.Errorf("unsupported installation settings action")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "INSTALL_SETTINGS_INVALID: enter a root HTTPS origin with the App gateway port; retained settings must be private and valid")
		os.Exit(1)
	}
}
