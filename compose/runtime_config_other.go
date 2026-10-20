//go:build !linux

package compose

import (
	"context"
	"fmt"
)

func writeRuntimeFile([]WritableDirectory, RuntimeFile) error {
	return fmt.Errorf("runtime configuration requires Linux")
}
func runPrepare(context.Context, []WritableDirectory, PrepareCommand) error {
	return fmt.Errorf("runtime preparation requires Linux")
}
