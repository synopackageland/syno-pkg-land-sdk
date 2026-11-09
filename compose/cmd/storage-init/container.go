package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"github.com/synopackageland/syno-pkg-land-sdk/compose"
)

const containerUsage = `synopkgland container: commands for container Apps (interactive console services)

Usage:
  synopkgland container fix-permissions [--home DIR]
      Make existing credential paths in HOME (~/.ssh, keys, ~/.gnupg, ~/.netrc,
      ~/.pgpass, ~/.kube/config, ~/.docker/config.json, ~/.aws/credentials)
      owner-only again. Only paths owned by you are changed; nothing is created
      and symlinks are not followed. Run it after a tool created one of them
      in a folder whose ACL widened the mode.
  synopkgland container init --config JSON -- COMMAND [ARG...]
      Entry mode used by the SDK-generated Compose file; not run by hand.
`

func containerCommand(args []string, stdout, stderr io.Writer) int {
	logf := func(line string) { fmt.Fprintln(stderr, line) }
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(stdout, containerUsage)
		if len(args) == 0 {
			return 2
		}
		return 0
	}
	switch args[0] {
	case "fix-permissions":
		home := os.Getenv("HOME")
		rest := args[1:]
		if len(rest) == 2 && rest[0] == "--home" {
			home, rest = rest[1], nil
		}
		if len(rest) != 0 || !strings.HasPrefix(home, "/") {
			fmt.Fprint(stderr, containerUsage)
			return 2
		}
		for _, p := range compose.FixPermissions(home, logf) {
			fmt.Fprintf(stdout, "tightened %s\n", p)
		}
		return 0
	case "init":
		if err := containerInit(args[1:], logf); err != nil {
			logf(err.Error())
			return 1
		}
		return 0
	}
	fmt.Fprint(stderr, containerUsage)
	return 2
}

func containerInit(args []string, logf func(string)) error {
	if len(args) < 4 || args[0] != "--config" || args[2] != "--" || len(args[1]) > 16<<10 {
		return fmt.Errorf("[COMPOSE_CONSOLE_INVALID] expected --config JSON -- COMMAND")
	}
	var c compose.ConsoleConfig
	dec := json.NewDecoder(strings.NewReader(args[1]))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return fmt.Errorf("[COMPOSE_CONSOLE_INVALID] invalid console declaration")
	}
	argv := args[3:]
	env, err := compose.InitializeConsole(context.Background(), c, logf)
	if err != nil {
		return err
	}
	for k, v := range env {
		os.Setenv(k, v)
	}
	os.Setenv("SYNOPKGLAND_UID", strconv.Itoa(os.Geteuid()))
	os.Setenv("SYNOPKGLAND_GID", strconv.Itoa(os.Getegid()))
	command, err := exec.LookPath(argv[0])
	if err != nil {
		return fmt.Errorf("[COMPOSE_STORAGE_COMMAND_INVALID] %w", err)
	}
	return syscall.Exec(command, argv, os.Environ())
}
