//go:build !linux

package compose

import "context"

type ConsoleEnv map[string]string

func InitializeConsole(context.Context, ConsoleConfig, func(string)) (ConsoleEnv, error) {
	return nil, problem("COMPOSE_CONSOLE_INVALID", "the console init runs only in Linux containers")
}

func FixPermissions(string, func(string)) []string { return nil }
