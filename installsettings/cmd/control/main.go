package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/synopackageland/syno-pkg-land-sdk/installsettings"
	"github.com/synopackageland/syno-pkg-land-sdk/internal/sharepreflight"
	"github.com/synopackageland/syno-pkg-land-sdk/sharedfolder"
	"os"
	"path/filepath"
	"syscall"
)

func main() {
	if len(os.Args) < 2 || os.Getuid() == 0 || os.Getuid() != os.Geteuid() {
		fmt.Fprintln(os.Stderr, "INSTALL_SETTINGS_INVALID: package account required")
		os.Exit(1)
	}
	fs := flag.NewFlagSet("install-settings", flag.ExitOnError)
	root := fs.String("root", "", "package payload")
	pkg := fs.String("package", "", "package ID")
	key := fs.String("key", "", "declared folder key")
	fs.Parse(os.Args[2:])
	if len(os.Args[1]) >= 7 && os.Args[1][:7] == "folder-" {
		folderAction(os.Args[1], *root, *pkg, *key)
		return
	}
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

func folderAction(action, root, pkg, key string) {
	var err error
	var path string
	switch action {
	case "folder-prepare":
		err = sharepreflight.PrepareNative(root, os.Getenv("SYNOPKG_PKGVAR"), pkg, "/")
	case "folder-check":
		err = sharepreflight.CheckPackage(root, os.Getenv("SYNOPKG_PKGVAR"))
	case "folder-path":
		path, err = sharepreflight.NativePath(root, os.Getenv("SYNOPKG_PKGVAR"), key, "/")
	default:
		err = fmt.Errorf("unsupported shared folder action")
	}
	if err == nil {
		if action == "folder-path" {
			json.NewEncoder(os.Stdout).Encode(map[string]string{"path": path})
		}
		return
	}
	message := sharedfolder.Message(err, os.Getenv("SYNOPKG_DSM_LANGUAGE"))
	fmt.Fprintln(os.Stderr, message)
	if action == "folder-path" {
		code := "shared_folder_unavailable"
		if errors.Is(err, sharedfolder.ErrPermissionPending) {
			code = "shared_folder_permission"
		}
		if errors.Is(err, sharedfolder.ErrIdentity) {
			code = "shared_folder_identity"
		}
		if errors.Is(err, sharedfolder.ErrNotConfigured) {
			code = "shared_folder_not_configured"
		}
		json.NewEncoder(os.Stdout).Encode(map[string]any{"error": map[string]string{"code": code, "message": message}})
	}
	if logfile := os.Getenv("SYNOPKG_TEMP_LOGFILE"); logfile != "" {
		if f, e := os.OpenFile(logfile, os.O_WRONLY|os.O_APPEND|os.O_CREATE|syscall.O_NOFOLLOW, 0600); e == nil {
			fmt.Fprintln(f, message)
			f.Close()
		}
	}
	var selection *sharedfolder.SelectionError
	if action != "folder-prepare" && (errors.Is(err, sharedfolder.ErrPermissionPending) || errors.As(err, &selection)) {
		os.Exit(2)
	}
	os.Exit(1)
}
