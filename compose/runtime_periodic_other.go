//go:build !linux

package compose

import (
	"context"
	"fmt"
)

func RunWithPeriodic(context.Context, []string, []PeriodicCommand) error {
	return fmt.Errorf("periodic container commands require Linux")
}
